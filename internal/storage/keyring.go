package storage

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/99designs/keyring"
)

const refPrefix = "ref://"

type Config struct {
	ConfigDir   string
	FileDir     string
	CertDir     string
	CAPath      string
	CAKeyPath   string
	ProxyLogDir string
}

type Store struct {
	ring keyring.Keyring
	cfg  Config
}

func NewStore(serviceName string) (*Store, error) {
	cfg, err := appConfig()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.FileDir, 0o700); err != nil {
		return nil, fmt.Errorf("create keyring dir: %w", err)
	}
	if err := os.MkdirAll(cfg.CertDir, 0o700); err != nil {
		return nil, fmt.Errorf("create cert dir: %w", err)
	}
	if err := os.MkdirAll(cfg.ProxyLogDir, 0o700); err != nil {
		return nil, fmt.Errorf("create proxy log dir: %w", err)
	}

	kr, err := keyring.Open(keyring.Config{
		ServiceName: serviceName,
		FileDir:     cfg.FileDir,
		FilePasswordFunc: func(_ string) (string, error) {
			return "reflet-poc-file-backend", nil
		},
		AllowedBackends: []keyring.BackendType{
			keyring.KeychainBackend,
			keyring.SecretServiceBackend,
			keyring.WinCredBackend,
			keyring.FileBackend,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("open keyring: %w", err)
	}

	return &Store{ring: kr, cfg: cfg}, nil
}

func (s *Store) Config() (Config, error) {
	return s.cfg, nil
}

func (s *Store) Set(name, value string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if value == "" {
		return errors.New("secret value cannot be empty")
	}
	return s.ring.Set(keyring.Item{
		Key:         name,
		Data:        []byte(value),
		Label:       name,
		Description: "reflet secret reference",
	})
}

func (s *Store) Get(name string) (string, error) {
	item, err := s.ring.Get(name)
	if err != nil {
		return "", fmt.Errorf("load secret %q: %w", name, err)
	}
	return string(item.Data), nil
}

func (s *Store) ResolveRef(ref string) (string, error) {
	if !strings.HasPrefix(ref, refPrefix) {
		return "", fmt.Errorf("invalid ref %q", ref)
	}
	return s.Get(strings.TrimPrefix(ref, refPrefix))
}

func (s *Store) List() ([]string, error) {
	keys, err := s.ring.Keys()
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	sort.Strings(keys)
	return keys, nil
}

func (s *Store) Remove(name string) error {
	if err := s.ring.Remove(name); err != nil {
		return fmt.Errorf("remove secret %q: %w", name, err)
	}
	return nil
}

func PromptSecret(name string) (string, error) {
	fmt.Fprintf(os.Stderr, "Enter secret for %s: ", name)
	reader := bufio.NewReader(os.Stdin)
	value, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read secret: %w", err)
	}
	secret := strings.TrimSpace(value)
	if secret == "" {
		return "", errors.New("secret value cannot be empty")
	}
	return secret, nil
}

func validateName(name string) error {
	if name == "" {
		return errors.New("name cannot be empty")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("._-/", r):
		default:
			return fmt.Errorf("invalid secret name %q", name)
		}
	}
	return nil
}

func appConfig() (Config, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return Config{}, fmt.Errorf("find user config dir: %w", err)
	}

	root := filepath.Join(base, "reflet")
	if runtime.GOOS == "windows" {
		root = filepath.Join(base, "Reflet")
	}

	return Config{
		ConfigDir:   root,
		FileDir:     filepath.Join(root, "keyring"),
		CertDir:     filepath.Join(root, "certs"),
		CAPath:      filepath.Join(root, "certs", "reflet-ca.pem"),
		CAKeyPath:   filepath.Join(root, "certs", "reflet-ca-key.pem"),
		ProxyLogDir: filepath.Join(root, "logs"),
	}, nil
}
