// Package secrets gestiona la llave local y el cifrado AES-256-GCM de los
// secretos guardados en state.db (tokens de conectores).
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const keySize = 32

// LoadOrCreateKey lee la llave de path o, si no existe, la genera (32 bytes, 0600).
// Una llave existente con tamaño incorrecto o permisos abiertos a grupo/otros es un error:
// no se corrige en silencio porque pudo haber quedado expuesta.
func LoadOrCreateKey(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return createKey(path)
	}
	if err != nil {
		return nil, err
	}
	if m := fi.Mode().Perm(); m&0o077 != 0 {
		return nil, fmt.Errorf("llave %s con permisos %04o: deben ser 0600 (chmod 600)", path, m)
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("llave %s inválida: %d bytes, se esperaban %d", path, len(key), keySize)
	}
	return key, nil
}

func createKey(path string) ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// CreateTemp crea con 0600; rename deja la llave completa o nada.
	f, err := os.CreateTemp(dir, ".secret-*.tmp")
	if err != nil {
		return nil, err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op tras un rename exitoso
	if _, err := f.Write(key); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return key, nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// Seal cifra plain y devuelve nonce||ciphertext.
func Seal(key, plain []byte) ([]byte, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("llave de %d bytes, se esperaban %d", len(key), keySize)
	}
	a, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, plain, nil), nil
}

// Open descifra lo producido por Seal; falla si la llave es otra o los datos fueron alterados.
func Open(key, sealed []byte) ([]byte, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("llave de %d bytes, se esperaban %d", len(key), keySize)
	}
	a, err := gcm(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < a.NonceSize()+a.Overhead() {
		return nil, errors.New("secreto cifrado demasiado corto")
	}
	n := a.NonceSize()
	plain, err := a.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return nil, fmt.Errorf("descifrar secreto: %w", err)
	}
	return plain, nil
}
