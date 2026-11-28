// Editor en línea del lector: CodeMirror 6 sobre la nota, con autosave.
// Se monta tras cada navegación parcial vía window.scribaInline.mount().
import { EditorState, Compartment, Annotation, Prec, Transaction } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";
import { history, historyKeymap, defaultKeymap, indentWithTab } from "@codemirror/commands";
import { markdownLanguage, markdownKeymap } from "@codemirror/lang-markdown";
import { syntaxTree, ensureSyntaxTree } from "@codemirror/language";
import { livePreview, slug, frontmatterEnd } from "./livepreview.js";

const DEBOUNCE = 1500;
// Marca las transacciones remotas (cargar versión del servidor) para que no disparen autosave.
const remote = Annotation.define();
// lock guarda el nombre del agente y "" significa editable; el 423 puede venir sin nombre.
const AGENTE_DESCONOCIDO = "?";

const TEXTOS = {
  saved: "Guardado",
  saving: "Guardando…",
  offline: "Sin conexión, reintentando",
  conflict: "Pausado: conflicto",
  locked: "Solo lectura",
};

let current = null; // instancia activa (una por nota)

// Sobreviven a la navegación parcial (mismo documento): url de la nota → …
const pendientes = new Map(); // promesa del PUT propio en vuelo (resuelve al terminar, tras relanzar `again`)
const confirmados = new Map(); // último PUT 200 propio: { base, t } (eco de una instancia anterior y atrás/adelante)
const claveBorrador = (url) => "scriba:borrador:" + url;

function mount() {
  // Siempre fuera la instancia anterior (navegación parcial incluida).
  if (current) {
    current.destroy();
    current = null;
  }

  const datos = document.getElementById("nota-editable");
  const reader = document.querySelector(".reader");
  const prose = reader && reader.querySelector(".prose");
  if (!datos || !prose) return;

  // htmx puede restaurar del historial una instantánea con el editor ya montado:
  // fuera restos de montajes anteriores (host, indicador, banner) antes de crear los nuestros.
  for (const resto of reader.querySelectorAll(".inline-editor, .inline-status, .inline-banner")) {
    resto.remove();
  }

  let nota;
  try {
    nota = JSON.parse(datos.textContent);
  } catch {
    return;
  }

  const baseUrl = document.body.dataset.base || "";
  const ruta = reader.dataset.note || "";
  const url = baseUrl + "/n/" + ruta.split("/").map(encodeURIComponent).join("/");
  const root = reader.closest(".view"); // contenedor con scroll de la vista

  // El HTML del servidor se pinta primero y queda oculto: live.js lo sigue actualizando
  // y vuelve a verse cuando un agente toma la nota (bloqueo) o al desmontar.
  prose.hidden = true;
  const host = document.createElement("div");
  host.className = "prose inline-editor";
  prose.after(host);

  const status = document.createElement("span");
  status.className = "inline-status";
  status.setAttribute("role", "status");
  status.setAttribute("aria-live", "polite");
  const meta = reader.querySelector(".meta");
  if (meta) meta.append(status);

  const ctrl = new AbortController();
  const { signal } = ctrl;

  let destroyed = false;
  let base = nota.base; // último base confirmado por el servidor
  let saved = ""; // último contenido confirmado (se fija tras crear la vista)
  let docNow = ""; // texto actual del doc (lo mantienen updateListener y los reemplazos remotos)
  let inflight = false;
  let again = false;
  let force = "";
  let paused = false;
  let timer = 0;
  let retryTimer = 0;
  let unlockTimer = 0;
  let retry = 0;
  let lock = ""; // agente que bloquea la nota; "" = editable
  let banner = null;
  let bannerBorrador = false;
  let echo = null; // changed web recibido con un PUT en vuelo
  const propios = new Set(); // bases que devolvieron los PUT 200 de esta pestaña
  let cabCache = null; // caché de encabezados(), invalidada en cada docChanged
  let seccionRaf = 0;

  const dirty = () => docNow !== saved || inflight;

  // localStorage puede lanzar en modo privado: siempre en try/catch.
  function guardarBorrador() {
    try {
      localStorage.setItem(claveBorrador(url), JSON.stringify({ raw: docNow, base }));
    } catch {}
  }
  function leerBorrador() {
    try {
      return JSON.parse(localStorage.getItem(claveBorrador(url)) || "null");
    } catch {
      return null;
    }
  }
  function borrarBorrador() {
    try {
      localStorage.removeItem(claveBorrador(url));
    } catch {}
  }

  const setState = (estado, texto) => {
    status.dataset.state = estado;
    status.textContent = texto || TEXTOS[estado];
  };

  const roComp = new Compartment();
  const roExt = (ro) => [EditorState.readOnly.of(ro), EditorView.editable.of(!ro)];

  const view = new EditorView({
    parent: host,
    doc: nota.raw,
    extensions: [
      history(),
      keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
      Prec.high(
        keymap.of([
          {
            key: "Mod-s",
            run: () => {
              clearTimeout(timer);
              save();
              return true; // el navegador no abre «Guardar página»
            },
          },
        ])
      ),
      // Sin markdown() para no empaquetar lang-html/css/js.
      markdownLanguage,
      keymap.of(markdownKeymap),
      EditorView.lineWrapping,
      livePreview({ links: nota.links || {}, base: baseUrl }),
      roComp.of(roExt(false)),
      EditorView.contentAttributes.of({
        "aria-label": "Contenido de la nota",
        "aria-multiline": "true",
        spellcheck: "true",
        lang: "es",
      }),
      EditorView.updateListener.of((update) => {
        if (update.docChanged) {
          docNow = update.state.doc.toString();
          cabCache = null;
          if (!update.transactions.some((tr) => tr.annotation(remote))) {
            if (bannerBorrador) quitarBanner(); // el borrador queda guardado y se ofrece en la próxima visita
            clearTimeout(timer);
            timer = setTimeout(save, DEBOUNCE);
          }
        }
        if (update.viewportChanged || update.docChanged || update.geometryChanged) pedirSeccion();
      }),
    ],
  });

  function quitarBanner() {
    if (banner) {
      banner.remove();
      banner = null;
    }
    bannerBorrador = false;
  }

  // Un solo banner a la vez; si hay botones, el foco va al primero.
  function mostrarBanner(texto, botones) {
    quitarBanner();
    const el = document.createElement("div");
    el.className = "changes inline-banner";
    el.setAttribute("role", "alert");
    el.append(document.createTextNode(texto));
    for (const { label, onClick } of botones) {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "btn";
      btn.textContent = label;
      btn.addEventListener("click", onClick, { signal });
      el.append(" ", btn);
    }
    prose.before(el); // la prosa va justo antes del host: arriba con el editor o con la prosa visible
    banner = el;
    const primero = el.querySelector("button");
    if (primero) primero.focus();
  }

  function bannerConflicto(actual, base409) {
    mostrarBanner("La nota cambió mientras la editabas.", [
      {
        label: "Cargar la versión actual",
        onClick: () => {
          view.dispatch({
            changes: { from: 0, to: view.state.doc.length, insert: actual },
            annotations: [remote.of(true), Transaction.addToHistory.of(false)],
          });
          base = base409;
          saved = docNow; // el updateListener ya dejó docNow con el doc nuevo (normalizado a \n)
          paused = false;
          force = "";
          quitarBanner();
          setState("saved");
          view.focus();
        },
      },
      {
        label: "Sobrescribir con la mía",
        onClick: () => {
          force = base409;
          paused = false;
          quitarBanner();
          save();
          view.focus();
        },
      },
    ]);
  }

  // Lo escrito con lock o conflicto que no llegó al servidor al navegar (ver destroy).
  function ofrecerBorrador() {
    const b = leerBorrador();
    if (!b || typeof b.raw !== "string") return;
    if (b.raw === docNow) {
      borrarBorrador();
      return;
    }
    mostrarBanner("Tienes texto sin guardar de la última vez que editaste esta nota.", [
      {
        label: "Recuperar",
        onClick: () => {
          borrarBorrador();
          quitarBanner();
          view.dispatch({
            changes: { from: 0, to: view.state.doc.length, insert: b.raw },
            annotations: [remote.of(true)], // sin autosave diferido: guardamos ya, abajo
          });
          base = b.base || base; // si la nota cambió desde entonces, el servidor responde 409 → conflicto
          clearTimeout(timer);
          save();
          view.focus();
        },
      },
      {
        label: "Descartar",
        onClick: () => {
          borrarBorrador();
          quitarBanner();
          view.focus();
        },
      },
    ]);
    bannerBorrador = true;
  }

  // Un agente escribe la nota: el editor se oculta y live.js sigue reproduciendo la prosa.
  function bloquearPorAgente(agente) {
    if (!agente) agente = AGENTE_DESCONOCIDO;
    if (lock === agente) return;
    if (!lock && !paused && docNow !== saved) {
      clearTimeout(timer);
      save(); // flush antes de bloquear; sale porque lock todavía es ""
    }
    lock = agente;
    clearTimeout(timer);
    view.dispatch({ effects: roComp.reconfigure(roExt(true)) });
    host.hidden = true;
    prose.hidden = false;
    setState("locked");
    mostrarBanner(
      agente === AGENTE_DESCONOCIDO
        ? "Un agente está escribiendo en esta nota"
        : `${agente} está escribiendo en esta nota`,
      []
    );
  }

  // El agente soltó la nota: pide los datos y vuelve al editor (o a conflicto si había
  // cambios locales sin confirmar).
  async function desbloquear() {
    if (!lock || destroyed) return;
    let data;
    try {
      const res = await fetch(url + "?datos=1", { headers: { Accept: "application/json" }, signal });
      if (!res.ok) throw new Error("datos: " + res.status);
      data = await res.json();
    } catch {
      // Sin soltar el lock: reintenta mientras la instancia viva.
      if (!destroyed) unlockTimer = setTimeout(desbloquear, 3000);
      return;
    }
    if (destroyed) return;
    // El agente volvió a escribir mientras llegaban los datos: sigue bloqueada.
    if ((window.scribaPresence || []).some((x) => x.path === ruta)) return;
    if (data.agent) {
      bloquearPorAgente(data.agent); // sigue escribiendo (no hace nada si es el mismo)
      return;
    }
    lock = "";
    quitarBanner();
    view.dispatch({ effects: roComp.reconfigure(roExt(false)) });
    prose.hidden = true;
    host.hidden = false;
    if (paused || docNow !== saved) {
      paused = true;
      setState("conflict");
      bannerConflicto(data.raw, data.base);
    } else {
      recargarCon(data);
      ofrecerBorrador();
    }
  }

  // Reemplaza el doc con la versión del servidor si difiere (sin mover el foco).
  function recargarCon(data) {
    const raw = String(data.raw ?? "").replace(/\r\n/g, "\n");
    if (raw !== docNow) {
      view.dispatch({
        changes: { from: 0, to: view.state.doc.length, insert: raw },
        annotations: [remote.of(true), Transaction.addToHistory.of(false)],
      });
    }
    base = data.base;
    saved = docNow;
    paused = false;
    force = "";
    setState("saved");
  }

  async function recargar() {
    try {
      const res = await fetch(url + "?datos=1", { headers: { Accept: "application/json" }, signal });
      if (!res.ok) return;
      const data = await res.json();
      if (destroyed) return;
      if (dirty()) return; // se escribió durante el GET
      if (data.agent) bloquearPorAgente(data.agent);
      else recargarCon(data);
    } catch {
      // sin red: el próximo evento lo vuelve a intentar
    }
  }

  // Cambio de otro (agente u otra pestaña): recarga si estamos limpios; si hay cambios,
  // guarda ya y el servidor decide (409 → conflicto, 423 → solo lectura).
  function ajeno() {
    if (!dirty()) recargar();
    else {
      clearTimeout(timer);
      save();
    }
  }

  function offline() {
    if (destroyed) return;
    setState("offline");
    retry = Math.min(retry ? retry * 2 : 2000, 30000);
    clearTimeout(retryTimer);
    retryTimer = setTimeout(save, retry);
  }

  async function save(opts = {}) {
    const final = destroyed || opts.final;
    if (paused || lock) return;
    if (inflight) {
      again = true;
      return;
    }
    const content = docNow;
    if (content === saved && !force) {
      if (!destroyed) setState("saved");
      return;
    }
    inflight = true;
    let fin;
    const hecho = new Promise((r) => (fin = r));
    pendientes.set(url, hecho);
    if (!destroyed) setState("saving");
    const body = JSON.stringify(force ? { content, base, force } : { content, base });
    try {
      // Sin signal: destruir la instancia no debe cancelar un guardado en vuelo.
      const res = await fetch(final ? url + "?flush=1" : url, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body,
        // shortcut: keepalive admite 64 KiB; una nota más grande depende del fetch normal y del aviso beforeunload
        keepalive: (final || opts.keepalive) && new Blob([body]).size <= 60000,
        priority: final ? "high" : "low", // sync en segundo plano: no compite con la navegación
      });
      const data = await res.json().catch(() => ({}));
      if (res.ok) {
        base = data.base;
        propios.add(base);
        confirmados.set(url, { base, t: Date.now() });
        if (propios.size > 20) propios.delete(propios.values().next().value);
        saved = content;
        if (destroyed && content === docNow) borrarBorrador(); // destroy lo guardó con lock, pero el servidor lo aceptó
        force = "";
        retry = 0;
        if (!destroyed) setState("saved");
      } else if (res.status === 409) {
        paused = true;
        // Bloqueada o destruida: el conflicto se muestra al desbloquear.
        if (!lock && !destroyed) {
          setState("conflict");
          bannerConflicto(data.current ?? "", data.base ?? base);
        }
        if (destroyed) guardarBorrador();
      } else if (res.status === 423) {
        paused = true;
        if (!destroyed) bloquearPorAgente(data.agent || "");
        else guardarBorrador();
      } else if (res.status === 400 || res.status === 404 || res.status === 413) {
        if (!destroyed) setState("error", data.error || "Error al guardar"); // no reintenta hasta el siguiente cambio
      } else if (res.status >= 500) {
        offline();
      } else if (!destroyed) {
        setState("error", data.error || "Error al guardar");
      }
    } catch {
      offline();
    } finally {
      inflight = false;
      if (echo) {
        const e = echo;
        echo = null;
        if (!propios.has(e.hash)) ajeno();
      }
      if (again) {
        again = false;
        save();
      }
      if (pendientes.get(url) === hecho) pendientes.delete(url);
      fin();
    }
  }

  // Títulos del doc con su id de ancla: mismo slug que el servidor y sufijos -1, -2… en repetidos.
  function encabezados() {
    if (cabCache) return cabCache;
    const state = view.state;
    const arbol = ensureSyntaxTree(state, state.doc.length, 200) || syntaxTree(state);
    // El frontmatter no se parsea como YAML: el "---" de cierre saldría como SetextHeading.
    const fmFin = frontmatterEnd(state.doc.toString());
    const usados = new Map();
    const lista = [];
    arbol.iterate({
      enter(n) {
        if (fmFin >= 0 && n.to <= fmFin) return false;
        const nombre = n.name;
        let texto;
        if (nombre.startsWith("ATXHeading")) {
          const marcas = n.node.getChildren("HeaderMark");
          const linea = state.doc.lineAt(n.from);
          texto = (
            marcas.length > 1
              ? state.sliceDoc(marcas[0].to, marcas[marcas.length - 1].from)
              : linea.text.slice(marcas.length ? marcas[0].to - linea.from : 0)
          ).trim();
        } else if (nombre.startsWith("SetextHeading")) {
          texto = state.doc.lineAt(n.from).text.trim(); // la primera línea
        } else {
          return;
        }
        let id = slug(texto);
        const veces = usados.get(id) || 0;
        usados.set(id, veces + 1);
        if (veces) id += "-" + veces;
        lista.push({ id, pos: n.from });
        return false;
      },
    });
    cabCache = lista;
    return lista;
  }

  // Marca en "En esta nota" la sección visible con el editor (la prosa está oculta y el
  // IntersectionObserver del lector no dispara). Agrupa en un rAF.
  function marcarSeccion() {
    seccionRaf = 0;
    if (destroyed || lock || !root) return;
    const enlaces = reader.querySelectorAll(".aside a.toc");
    // Solo los títulos del índice: el servidor deja fuera el H1 inicial y los niveles > 3.
    const ids = new Set([...enlaces].map((a) => a.dataset.id));
    const lista = encabezados().filter((h) => ids.has(h.id));
    if (!lista.length || !enlaces.length) return;
    const rt = root.getBoundingClientRect().top;
    const limite = root.clientHeight * 0.3;
    const arriba = (h) => view.lineBlockAt(h.pos).top + view.documentTop - rt; // top del título respecto a .view
    let activo = lista[0]; // si ninguno pasa el umbral, el primero
    for (const h of lista) {
      if (arriba(h) <= limite) activo = h;
      else break;
    }
    // Al final de la nota las últimas secciones no llegan al umbral: gana el último título visible.
    if (root.scrollTop > 0 && root.scrollTop + root.clientHeight >= root.scrollHeight - 2) {
      for (const h of lista) if (arriba(h) < root.clientHeight) activo = h;
    }
    let actual = null;
    for (const a of enlaces) {
      const on = a.dataset.id === activo.id;
      a.classList.toggle("on", on);
      if (on) actual = a;
    }
    if (!actual) return;
    // Mantiene el enlace activo visible en el panel (misma lógica que el script del lector).
    const aside = actual.closest(".aside");
    const ar = aside.getBoundingClientRect();
    const lr = actual.getBoundingClientRect();
    if (lr.top < ar.top || lr.bottom > ar.bottom) aside.scrollTop += lr.top - ar.top - ar.height / 2;
  }

  function pedirSeccion() {
    if (!seccionRaf) seccionRaf = requestAnimationFrame(marcarSeccion);
  }

  // CodeMirror normaliza \r\n a \n al crear el doc: el «guardado» de referencia
  // es el doc normalizado, no el archivo tal cual (si no, Mod-s sin cambios reescribe).
  docNow = view.state.doc.toString();
  saved = docNow;

  // Anclas del índice (.aside a.toc) y wikilinks [[#Sección]] (a.wl con href="#…"): con el
  // editor visible las resolvemos contra el doc de CM6; con lock la prosa es visible y el
  // navegador las resuelve solo. En capture para ganarle al clic del widget wikilink.
  document.addEventListener(
    "click",
    (e) => {
      if (lock) return;
      if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      const a = e.target.closest?.('a[href^="#"]');
      if (!a || !a.closest(".reader")) return;
      const id = decodeURIComponent(a.getAttribute("href").slice(1));
      const h = encabezados().find((x) => x.id === id);
      if (!h) return;
      e.preventDefault();
      e.stopPropagation();
      view.dispatch({ effects: EditorView.scrollIntoView(h.pos, { y: "start", yMargin: 16 }) });
      window.history.replaceState(window.history.state, "", "#" + encodeURIComponent(id)); // history es el de CM6
    },
    { capture: true, signal }
  );

  // Presencia de agentes (la despacha live.js desde el SSE).
  addEventListener(
    "scriba:presence",
    (e) => {
      const a = (e.detail || []).find((x) => x.path === ruta);
      if (a) bloquearPorAgente(a.agent);
      else if (lock) desbloquear();
    },
    { signal }
  );
  if (root) root.addEventListener("scroll", pedirSeccion, { passive: true, signal });
  // Al salir o esconder la pestaña, el guardado pendiente sale sin esperar el debounce.
  addEventListener(
    "pagehide",
    () => {
      clearTimeout(timer);
      save({ final: true });
    },
    { signal }
  );
  document.addEventListener(
    "visibilitychange",
    () => {
      if (document.visibilityState === "hidden") {
        clearTimeout(timer);
        save({ keepalive: true });
      }
    },
    { signal }
  );
  addEventListener(
    "beforeunload",
    (e) => {
      // También con lock o conflicto: lo escrito sigue sin guardar en el editor oculto.
      if (dirty()) e.preventDefault();
    },
    { signal }
  );

  setState("saved");
  if (nota.agent) {
    bloquearPorAgente(nota.agent);
    // La presencia pudo vencer entre el render y el montaje: el SSE ya no la reenvía.
    if (window.scribaPresence && !window.scribaPresence.some((x) => x.path === ruta)) desbloquear();
  } else {
    // Por si el SSE ya anunció a un agente en esta ruta antes de montar el editor.
    const a = (window.scribaPresence || []).find((x) => x.path === ruta);
    if (a) bloquearPorAgente(a.agent);
  }

  // Ancla inicial (enlace con #sección): desplázate al título tras montar.
  if (location.hash.length > 1) {
    const id = decodeURIComponent(location.hash.slice(1));
    requestAnimationFrame(() => {
      if (destroyed || lock) return;
      const h = encabezados().find((x) => x.id === id);
      if (h) view.dispatch({ effects: EditorView.scrollIntoView(h.pos, { y: "start", yMargin: 16 }) });
    });
  }
  pedirSeccion();

  // Atrás/adelante: el GET de la página pudo salir antes de que terminara el PUT propio del
  // destroy anterior y traer un base viejo (→ 409 contra el texto propio). Espera a que no
  // quede ninguno en vuelo y recarga si el editor sigue limpio. También si el último PUT
  // propio, de hace menos de 10 s, no es el base de la página (el GET leyó antes de que escribiera).
  const conf = confirmados.get(url);
  if (pendientes.has(url) || (conf && conf.base !== nota.base && Date.now() - conf.t < 10000)) {
    (async () => {
      let p;
      while ((p = pendientes.get(url))) await p;
      if (!destroyed && !lock && !dirty()) await recargar();
      if (!destroyed && !lock) ofrecerBorrador();
    })();
  } else if (!lock) {
    ofrecerBorrador();
  }

  current = {
    destroy() {
      destroyed = true;
      clearTimeout(timer);
      clearTimeout(retryTimer);
      clearTimeout(unlockTimer);
      if (seccionRaf) cancelAnimationFrame(seccionRaf);
      if (docNow !== saved) {
        // Con lock o conflicto el servidor no lo acepta (423/409): queda como borrador local
        // y se ofrece al volver a la nota. Si el PUT final vuelve 409/423, save() hace lo mismo.
        if (paused || lock) guardarBorrador();
        else if (inflight) again = true; // el PUT en vuelo lo relanza al terminar (con flush)
        else save(); // destroyed → final: keepalive + ?flush=1
      }
      ctrl.abort(); // listeners y fetch de datos en vuelo
      view.destroy();
      quitarBanner();
      status.remove();
      host.remove();
      prose.hidden = false; // el HTML del servidor vuelve a ser visible
    },
    // Un changed del SSE para esta nota: true si el editor se encarga (live.js no refresca).
    changed(ev) {
      if (destroyed || lock) return false; // bloqueado: live.js refresca y reproduce la prosa
      if (ev.kind !== "changed") return false;
      // Un agente: su presencia llega justo después y bloquea; live.js reproduce la prosa ya.
      // Lo pendiente sale antes (423/409 → conflicto al desbloquear).
      if (ev.agent && ev.agent !== "web") {
        if (dirty()) {
          clearTimeout(timer);
          save();
        }
        return false;
      }
      if (ev.agent === "web") {
        if (inflight) {
          echo = ev;
          return true;
        }
        if (propios.has(ev.hash) || ev.hash === base || ev.hash === confirmados.get(url)?.base) return true; // eco propio
      }
      ajeno();
      return true;
    },
  };
}

window.scribaInline = { mount, changed: (ev) => (current ? current.changed(ev) : false) };

// Navegación parcial (htmx): destruye el editor antes de cambiar la vista para que el último
// guardado salga con flush. El script del layout (inline al final del body) escucha el mismo
// evento y se registró antes —este bundle se carga después, desde el lector—, así que su
// preventDefault ya se ve en e.defaultPrevented.
document.addEventListener("htmx:before:swap", (e) => {
  if (e.defaultPrevented || !e.detail?.ctx?.sourceElement?._htmx?.boosted) return;
  if (current) {
    current.destroy();
    current = null;
  }
});
document.addEventListener("htmx:before:history:restore", () => {
  if (current) {
    current.destroy();
    current = null;
  }
});

// En carga completa el script puede evaluarse antes de que exista .prose.
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", mount, { once: true });
} else {
  mount();
}
