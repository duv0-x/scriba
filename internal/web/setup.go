package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/duv0-x/scriba/internal/github"
	"github.com/duv0-x/scriba/internal/gitvault"
	"github.com/duv0-x/scriba/internal/onboarding"
	"github.com/duv0-x/scriba/internal/secrets"
	"github.com/duv0-x/scriba/internal/state"
)

// GitHubAPI es lo que la web usa de github.Client (interfaz para poder usar fakes en tests).
type GitHubAPI interface {
	StartDevice(ctx context.Context, clientID, scope string) (github.DeviceCode, error)
	PollToken(ctx context.Context, clientID, deviceCode string) (github.Token, error)
	User(ctx context.Context, token string) (string, error)
	Repos(ctx context.Context, token string) ([]github.Repo, error)
}

// GitOps es lo que la web usa de gitvault.
type GitOps interface {
	IsRepo(dir string) bool
	Init(dir string) error
	Clone(ctx context.Context, url, dir string, cred gitvault.Cred) error
	CommitAll(dir, author, msg string) (hash string, committed bool, err error)
	Remote(dir string) (string, error) // URL de origin; "" si no hay origin
	SetRemote(dir, url string) error   // agrega origin
}

// GitVault adapta las funciones de gitvault a GitOps.
type GitVault struct{}

func (GitVault) IsRepo(dir string) bool { return gitvault.IsRepo(dir) }
func (GitVault) Init(dir string) error  { return gitvault.Init(dir) }
func (GitVault) Clone(ctx context.Context, url, dir string, cred gitvault.Cred) error {
	return gitvault.Clone(ctx, url, dir, cred)
}
func (GitVault) CommitAll(dir, author, msg string) (string, bool, error) {
	return gitvault.CommitAll(dir, author, msg)
}

func (GitVault) Remote(dir string) (string, error) {
	out, err := gitRemote(dir, "get-url", "origin")
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) && ee.ExitCode() == 2 {
		return "", nil // git sale con 2 cuando no existe el remoto
	}
	return strings.TrimSpace(out), err
}

func (GitVault) SetRemote(dir, url string) error {
	_, err := gitRemote(dir, "add", "origin", url)
	return err
}

// gitRemote corre `git remote` sin config de sistema ni global ni fsmonitor (el vault puede venir de fuera).
func gitRemote(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "core.fsmonitor=false", "remote"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git remote %s: %w", args[0], err)
	}
	return string(out), nil
}

const (
	settingInitialized = "initialized"
	connGitHub         = "github"
	cloneTimeout       = 5 * time.Minute
)

const avisoHistorial = "El .zip traía historial de git (.git). Por seguridad no se importa: la base empieza con un historial nuevo. Para conservar el historial, importa desde GitHub en Conectores."

// pendingDevice es el device flow en curso; vive solo en memoria (el device_code nunca va al HTML).
type pendingDevice struct {
	deviceCode, userCode, uri string
	interval                  int
	expires                   time.Time
}

// setupRoutes registra bienvenida y conectores; solo existen si hay State.
func (s *Server) setupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /bienvenida", s.welcome)
	mux.HandleFunc("POST /bienvenida/nueva", s.welcomeNew)
	mux.HandleFunc("POST /bienvenida/zip", s.welcomeZip)
	mux.HandleFunc("GET /conectores", s.connectors)
	mux.HandleFunc("POST /conectores/github/iniciar", s.ghStart)
	mux.HandleFunc("GET /conectores/github/estado", s.ghPoll)
	mux.HandleFunc("POST /conectores/github/repo", s.ghRepo)
	mux.HandleFunc("POST /conectores/github/push", s.ghPush)
	mux.HandleFunc("POST /conectores/github/desconectar", s.ghDisconnect)
	mux.HandleFunc("POST /conectores/kindle", s.kindleSave)
	mux.HandleFunc("POST /conectores/kindle/prueba", s.kindleTest)
	mux.HandleFunc("POST /conectores/kindle/desconectar", s.kindleDisconnect)
	mux.HandleFunc("POST /kindle/{path...}", s.kindleNote)
}

// sameOrigin es la defensa CSRF de los POST: sin cookies de sesión en el MVP (la instancia va tras Access).
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		ok := false
		if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" {
			ok = sfs == "same-origin"
		} else if o, err := url.Parse(r.Header.Get("Origin")); err == nil && o.Host != "" {
			ok = o.Host == r.Host
		}
		if !ok {
			http.Error(w, "origen no permitido", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// gate manda a /bienvenida mientras la base esté vacía y sin inicializar.
func (s *Server) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if r.Method != http.MethodGet || strings.HasPrefix(p, "/static/") || p == "/readyz" ||
			strings.HasPrefix(p, "/bienvenida") || strings.HasPrefix(p, "/conectores") {
			next.ServeHTTP(w, r)
			return
		}
		init, err := s.initialized()
		if err != nil {
			serverError(w, err)
			return
		}
		if !init {
			http.Redirect(w, r, "/bienvenida", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) initialized() (bool, error) {
	if v, _, err := s.State.Setting(settingInitialized); err != nil || v == "1" {
		return v == "1", err
	}
	n, err := s.Ix.NoteCount()
	return n > 0, err
}

// markImported cierra la creación/importación: marca la base, reinicia los baselines de "sin
// leer" (las notas importadas no son novedades) y avisa para reindexar.
func (s *Server) markImported() error {
	if err := s.State.SetSetting(settingInitialized, "1"); err != nil {
		return err
	}
	if err := s.State.ClearBaselines(); err != nil {
		return err
	}
	if s.OnImported != nil {
		s.OnImported()
	}
	return nil
}

// vaultEmpty: el directorio del vault no tiene nada (salvo .DS_Store), que es lo que exigen Scaffold, ImportZip y Clone.
func (s *Server) vaultEmpty() bool {
	ents, err := os.ReadDir(s.V.Root)
	if err != nil {
		return os.IsNotExist(err)
	}
	for _, e := range ents {
		if e.Name() != ".DS_Store" {
			return false
		}
	}
	return true
}

// initRepo deja el vault como repo git con un commit inicial.
func (s *Server) initRepo() error {
	root := s.V.Root
	if !s.Git.IsRepo(root) {
		if err := s.Git.Init(root); err != nil {
			return err
		}
	}
	_, _, err := s.Git.CommitAll(root, "web", "Base de conocimiento inicial")
	return err
}

func (s *Server) welcome(w http.ResponseWriter, r *http.Request) {
	s.renderWelcome(w, r, http.StatusOK, "")
}

func (s *Server) renderWelcome(w http.ResponseWriter, r *http.Request, status int, msg string) {
	sh, err := s.shellFor(r, "")
	if err != nil {
		serverError(w, err)
		return
	}
	sh.View = "bienvenida"
	page(w, r, status, layout(sh, "Bienvenido · Scriba", welcomePage(msg)))
}

func (s *Server) welcomeNew(w http.ResponseWriter, r *http.Request) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	err := onboarding.Scaffold(s.V.Root) // Scaffold rechaza un vault con contenido
	if errors.Is(err, onboarding.ErrNotEmpty) {
		s.renderWelcome(w, r, http.StatusConflict, "La base ya tiene notas.")
		return
	}
	if err == nil {
		err = s.initRepo()
	}
	if err == nil {
		err = s.markImported()
	}
	if err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) welcomeZip(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, onboarding.MaxZipBytes+1<<20)
	// ParseMultipartForm pasa a un archivo temporal lo que no cabe en memoria; RemoveAll lo borra.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		if mbe := (*http.MaxBytesError)(nil); errors.As(err, &mbe) {
			s.renderWelcome(w, r, http.StatusRequestEntityTooLarge, "Supera 200 MB.")
			return
		}
		s.renderWelcome(w, r, http.StatusBadRequest, "No se pudo leer el archivo subido.")
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, hdr, err := r.FormFile("zip")
	if err != nil {
		s.renderWelcome(w, r, http.StatusBadRequest, "Elige un archivo .zip.")
		return
	}
	defer f.Close()

	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	res, err := onboarding.ImportZip(f, hdr.Size, s.V.Root) // ImportZip rechaza un vault con contenido
	switch {
	case errors.Is(err, onboarding.ErrNotEmpty):
		s.renderWelcome(w, r, http.StatusConflict, "La base ya tiene notas.")
		return
	case errors.Is(err, onboarding.ErrUnsafeZip):
		reason := strings.TrimPrefix(err.Error(), onboarding.ErrUnsafeZip.Error()+": ")
		s.renderWelcome(w, r, http.StatusBadRequest, "El .zip no es seguro: "+reason)
		return
	}
	if err == nil {
		err = s.initRepo()
	}
	if err == nil {
		err = s.markImported()
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if res.DroppedGit {
		http.Redirect(w, r, "/?aviso=historial", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ghView son los datos de la tarjeta de GitHub.
type ghView struct {
	Conn       state.Connector
	Connected  bool
	Wait       *pendingDevice
	Repos      []github.Repo
	Branch     string
	VaultEmpty bool
	NoClientID bool
	Msg        string
	Kindle     kindleView
}

func (s *Server) connectors(w http.ResponseWriter, r *http.Request) {
	s.renderConnectors(w, r, http.StatusOK, "")
}

func (s *Server) renderConnectors(w http.ResponseWriter, r *http.Request, status int, msg string) {
	v := ghView{VaultEmpty: s.vaultEmpty(), NoClientID: s.ClientID == "", Msg: msg}
	c, ok, tok, err := s.ghToken()
	if err != nil {
		serverError(w, err)
		return
	}
	switch {
	case ok:
		v.Conn, v.Connected = c, true
		if c.Repo == "" {
			repos, err := s.GitHub.Repos(r.Context(), tok)
			if err != nil {
				slog.Error("web: github repos", "err", err)
				v.Msg = "No pudimos listar tus repositorios de GitHub. Recarga la página para intentarlo de nuevo."
			}
			v.Repos = repos
		} else {
			v.Branch = branchOf(s.V.Root)
		}
	default:
		s.mu.Lock()
		if s.dev != nil {
			d := *s.dev
			v.Wait = &d
		}
		s.mu.Unlock()
	}
	kc, active, err := s.kindleConnector()
	if err != nil {
		serverError(w, err)
		return
	}
	v.Kindle = kindleView{To: kc.Account, From: kc.Repo, Active: active}
	sh, err := s.shellFor(r, "")
	if err != nil {
		serverError(w, err)
		return
	}
	sh.View = "conectores"
	page(w, r, status, layout(sh, "Conectores · Scriba", connectorsPage(v)))
}

// ghToken devuelve el conector de GitHub con su token descifrado; el token no debe salir del handler que lo pide.
func (s *Server) ghToken() (state.Connector, bool, string, error) {
	c, ok, err := s.State.KBConnector(connGitHub, s.KB)
	if err != nil || !ok {
		return c, false, "", err
	}
	plain, err := secrets.Open(s.Key, c.Secret)
	if err != nil {
		return c, false, "", fmt.Errorf("descifrar conector github: %w", err)
	}
	return c, true, string(plain), nil
}

func (s *Server) ghStart(w http.ResponseWriter, r *http.Request) {
	if s.ClientID == "" {
		s.renderConnectors(w, r, http.StatusServiceUnavailable, "")
		return
	}
	dc, err := s.GitHub.StartDevice(r.Context(), s.ClientID, "repo")
	if err != nil {
		slog.Error("web: github device", "err", err)
		s.renderConnectors(w, r, http.StatusBadGateway, "GitHub no respondió. Inténtalo de nuevo en un momento.")
		return
	}
	s.mu.Lock()
	s.dev = &pendingDevice{
		deviceCode: dc.DeviceCode, userCode: dc.UserCode, uri: dc.VerificationURI,
		interval: max(dc.Interval, 5), expires: time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second),
	}
	s.mu.Unlock()
	http.Redirect(w, r, "/conectores", http.StatusSeeOther)
}

// ghPoll es el sondeo de htmx: responde la tarjeta de espera o, con el token, HX-Redirect.
func (s *Server) ghPoll(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	var d pendingDevice
	if s.dev != nil {
		d = *s.dev
	}
	s.mu.Unlock()
	if d.deviceCode == "" {
		// Otra pestaña ya completó la conexión: que esta recargue en vez de mostrar un error.
		if _, ok, err := s.State.KBConnector(connGitHub, s.KB); err == nil && ok {
			w.Header().Set("HX-Redirect", "/conectores")
			w.WriteHeader(http.StatusOK)
			return
		}
		page(w, r, http.StatusOK, ghWaitFailed("No hay una conexión en curso."))
		return
	}
	if time.Now().After(d.expires) {
		s.clearDevice()
		page(w, r, http.StatusOK, ghWaitFailed("El código expiró antes de autorizarlo."))
		return
	}

	tok, err := s.GitHub.PollToken(r.Context(), s.ClientID, d.deviceCode)
	switch {
	case errors.Is(err, github.ErrSlowDown):
		s.mu.Lock()
		if s.dev != nil {
			s.dev.interval += 5
			d.interval = s.dev.interval
		}
		s.mu.Unlock()
		fallthrough
	case errors.Is(err, github.ErrPending):
		page(w, r, http.StatusOK, ghWaiting(d))
		return
	case errors.Is(err, github.ErrExpired):
		s.clearDevice()
		page(w, r, http.StatusOK, ghWaitFailed("El código expiró antes de autorizarlo."))
		return
	case errors.Is(err, github.ErrDenied):
		s.clearDevice()
		page(w, r, http.StatusOK, ghWaitFailed("Cancelaste la autorización en GitHub."))
		return
	case err != nil:
		// Fallo transitorio (red, 5xx): se sigue sondeando.
		slog.Error("web: github poll", "err", err)
		page(w, r, http.StatusOK, ghWaiting(d))
		return
	}

	login, err := s.GitHub.User(r.Context(), tok.AccessToken)
	if err != nil {
		slog.Error("web: github user", "err", err)
		s.clearDevice()
		page(w, r, http.StatusOK, ghWaitFailed("GitHub no devolvió la cuenta autorizada."))
		return
	}
	sealed, err := secrets.Seal(s.Key, []byte(tok.AccessToken))
	if err == nil {
		err = s.State.SaveKBConnector(s.KB, state.Connector{Name: connGitHub, Account: login, Secret: sealed, Push: true})
	}
	s.clearDevice()
	if err != nil {
		slog.Error("web: guardar conector github", "err", err)
		page(w, r, http.StatusOK, ghWaitFailed("No se pudo guardar la conexión. Inténtalo de nuevo."))
		return
	}
	w.Header().Set("HX-Redirect", "/conectores")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) clearDevice() {
	s.mu.Lock()
	s.dev = nil
	s.mu.Unlock()
}

// repoName valida owner/nombre de GitHub antes de meterlo en una URL.
var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func (s *Server) ghRepo(w http.ResponseWriter, r *http.Request) {
	repo := r.PostFormValue("repo")
	if !repoName.MatchString(repo) || strings.Contains(repo, "..") {
		s.renderConnectors(w, r, http.StatusBadRequest, "Elige un repositorio de la lista.")
		return
	}
	_, ok, tok, err := s.ghToken()
	if err != nil {
		serverError(w, err)
		return
	}
	if !ok {
		http.Redirect(w, r, "/conectores", http.StatusSeeOther)
		return
	}
	if !s.vaultEmpty() {
		s.ghLink(w, r, repo)
		return
	}

	// El clone va fuera del lock (puede tardar minutos) y desacoplado de la petición:
	// si el proxy corta la conexión, no queda a medias.
	ctx, cancel := context.WithTimeout(context.Background(), cloneTimeout)
	defer cancel()
	tmp, err := s.cloneTmp(ctx, "https://github.com/"+repo+".git", tok)
	if tmp != "" {
		defer os.RemoveAll(tmp)
	}
	if err != nil {
		slog.Error("web: clonar", "repo", repo, "err", err)
		msg := "No se pudo clonar " + repo + ". Revisa que la cuenta tenga acceso e inténtalo de nuevo."
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "Clonar " + repo + " tardó más de 5 minutos y se canceló. Inténtalo de nuevo o sube la base como .zip."
		}
		s.renderConnectors(w, r, http.StatusBadGateway, msg)
		return
	}

	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	c, ok, err := s.State.KBConnector(connGitHub, s.KB) // releer: pudo desconectarse durante el clone
	if err != nil {
		serverError(w, err)
		return
	}
	if !ok {
		s.renderConnectors(w, r, http.StatusConflict, "GitHub se desconectó durante la importación; no se importó nada.")
		return
	}
	if !s.vaultEmpty() {
		s.renderConnectors(w, r, http.StatusConflict, "La base ya tiene notas.")
		return
	}
	if err := moveInto(tmp, s.V.Root); err != nil {
		serverError(w, err)
		return
	}
	c.Repo = repo
	if err := s.State.SaveKBConnector(s.KB, c); err != nil {
		serverError(w, err)
		return
	}
	if err := s.markImported(); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ghLink enlaza un vault con contenido a repo: debe ser un repo git cuyo origin sea ese repo (o no tener origin).
func (s *Server) ghLink(w http.ResponseWriter, r *http.Request, repo string) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	c, ok, err := s.State.KBConnector(connGitHub, s.KB)
	if err != nil {
		serverError(w, err)
		return
	}
	if !ok {
		http.Redirect(w, r, "/conectores", http.StatusSeeOther)
		return
	}
	root := s.V.Root
	if s.vaultEmpty() || !s.Git.IsRepo(root) {
		s.renderConnectors(w, r, http.StatusConflict, "El vault no es un repositorio git; no se puede enlazar a "+repo+".")
		return
	}
	want := "https://github.com/" + repo + ".git"
	origin, err := s.Git.Remote(root)
	if err != nil {
		serverError(w, err)
		return
	}
	switch {
	case origin == "":
		if err := s.Git.SetRemote(root, want); err != nil {
			serverError(w, err)
			return
		}
	case normRemote(origin) != normRemote(want):
		s.renderConnectors(w, r, http.StatusConflict, "Este vault apunta a "+origin+"; no se puede enlazar a "+repo+".")
		return
	}
	c.Repo = repo
	if err := s.State.SaveKBConnector(s.KB, c); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/conectores", http.StatusSeeOther)
}

// normRemote compara URLs de GitHub sin mayúsculas ni .git final.
func normRemote(u string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(u)), ".git")
}

// cloneTmp clona en un directorio hermano del vault: el vault puede tener un .DS_Store y Clone
// exige un destino vacío; además, si algo falla, el vault queda intacto. El llamador borra tmp.
func (s *Server) cloneTmp(ctx context.Context, url, token string) (string, error) {
	tmp, err := os.MkdirTemp(filepath.Dir(s.V.Root), ".scriba-clone-")
	if err != nil {
		return "", err
	}
	return tmp, s.Git.Clone(ctx, url, tmp, gitvault.Cred{Token: token})
}

// moveInto mueve el contenido de tmp al vault.
func moveInto(tmp, root string) error {
	ents, err := os.ReadDir(tmp)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	for _, e := range ents {
		if err := os.Rename(filepath.Join(tmp, e.Name()), filepath.Join(root, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) ghPush(w http.ResponseWriter, r *http.Request) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	c, ok, err := s.State.KBConnector(connGitHub, s.KB)
	if err == nil && ok {
		c.Push = r.PostFormValue("push") == "1"
		err = s.State.SaveKBConnector(s.KB, c)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/conectores", http.StatusSeeOther)
}

func (s *Server) ghDisconnect(w http.ResponseWriter, r *http.Request) {
	s.clearDevice()
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if err := s.State.DeleteConnector(connGitHub); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/conectores", http.StatusSeeOther)
}

// branchOf lee la rama actual de .git/HEAD ("" si no es un repo o está en detached HEAD).
func branchOf(root string) string {
	b, err := os.ReadFile(filepath.Join(root, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	ref, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "ref: refs/heads/")
	if !ok {
		return ""
	}
	return ref
}

// pollEvery es el hx-trigger del sondeo según el intervalo que pide GitHub.
func pollEvery(d pendingDevice) string { return "every " + strconv.Itoa(d.interval) + "s" }

func boolAttr(b bool) string { return strconv.FormatBool(b) }

// verifyURL solo enlaza a GitHub por https; cualquier otra cosa cae en la página estándar del device flow.
func verifyURL(u string) templ.SafeURL {
	if strings.HasPrefix(u, "https://github.com/") {
		return templ.SafeURL(u)
	}
	return "https://github.com/login/device"
}

// toggleValue es el valor que envía el switch: el contrario del estado actual.
func toggleValue(on bool) string {
	if on {
		return "0"
	}
	return "1"
}
