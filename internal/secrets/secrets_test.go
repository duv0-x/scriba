package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "secret.key")
	k1, err := LoadOrCreateKey(p)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 || len(k1) != 32 {
		t.Fatalf("modo %04o, len %d", fi.Mode().Perm(), len(k1))
	}
	if di, _ := os.Stat(filepath.Dir(p)); di.Mode().Perm() != 0o700 {
		t.Fatalf("directorio con modo %04o", di.Mode().Perm())
	}
	k2, err := LoadOrCreateKey(p)
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatalf("segunda carga: %v, iguales=%v", err, bytes.Equal(k1, k2))
	}
}

func TestLoadKeyRechazos(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]struct {
		size int
		mode os.FileMode
	}{
		"permisos 0644": {32, 0o644},
		"permisos 0640": {32, 0o640},
		"31 bytes":      {31, 0o600},
		"33 bytes":      {33, 0o600},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, name)
			if err := os.WriteFile(p, make([]byte, c.size), c.mode); err != nil {
				t.Fatal(err)
			}
			os.Chmod(p, c.mode) // la umask pudo recortar el modo
			if _, err := LoadOrCreateKey(p); err == nil {
				t.Fatal("se esperaba error")
			}
			if fi, _ := os.Stat(p); fi.Mode().Perm() != c.mode {
				t.Fatalf("cambió los permisos a %04o", fi.Mode().Perm())
			}
		})
	}
}

func TestSealOpen(t *testing.T) {
	k1 := bytes.Repeat([]byte{1}, 32)
	k2 := bytes.Repeat([]byte{2}, 32)
	plain := []byte("gho_token")
	s, err := Seal(k1, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(s, plain) {
		t.Fatal("el texto plano aparece en el sellado")
	}
	if s2, _ := Seal(k1, plain); bytes.Equal(s, s2) {
		t.Fatal("dos sellados iguales: el nonce no es aleatorio")
	}
	got, err := Open(k1, s)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("ida y vuelta: %q, %v", got, err)
	}
	if _, err := Open(k2, s); err == nil {
		t.Fatal("llave distinta: se esperaba error")
	}
	bad := bytes.Clone(s)
	bad[len(bad)-1] ^= 1
	if _, err := Open(k1, bad); err == nil {
		t.Fatal("datos manipulados: se esperaba error")
	}
	for _, short := range [][]byte{nil, s[:11], s[:27]} {
		if _, err := Open(k1, short); err == nil {
			t.Fatalf("datos cortos (%d): se esperaba error", len(short))
		}
	}
	if _, err := Seal(k1[:31], plain); err == nil {
		t.Fatal("llave de 31 bytes: se esperaba error")
	}
}
