package keyring

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

type BackendType string

const (
	KeychainBackend      BackendType = "keychain"
	SecretServiceBackend BackendType = "secret-service"
	WinCredBackend       BackendType = "wincred"
	FileBackend          BackendType = "file"
)

type Config struct {
	ServiceName      string
	FileDir          string
	FilePasswordFunc func(prompt string) (string, error)
	AllowedBackends  []BackendType
}

type Item struct {
	Key         string
	Data        []byte
	Label       string
	Description string
}

type Keyring interface {
	Set(Item) error
	Get(key string) (Item, error)
	Remove(key string) error
	Keys() ([]string, error)
}

var ErrKeyNotFound = errors.New("key not found")

type fileRing struct {
	path string
	mu   sync.Mutex
}

type storedItem struct {
	Key         string `json:"key"`
	Data        string `json:"data"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

func Open(cfg Config) (Keyring, error) {
	if cfg.FileDir == "" {
		return nil, errors.New("keyring FileDir is required")
	}
	if err := os.MkdirAll(cfg.FileDir, 0o700); err != nil {
		return nil, fmt.Errorf("create keyring dir: %w", err)
	}
	return &fileRing{path: filepath.Join(cfg.FileDir, "reflet-keyring.json")}, nil
}

func (r *fileRing) Set(item Item) error {
	if item.Key == "" {
		return errors.New("item key is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	items, err := r.readAll()
	if err != nil {
		return err
	}
	items[item.Key] = storedItem{
		Key:         item.Key,
		Data:        base64.StdEncoding.EncodeToString(item.Data),
		Label:       item.Label,
		Description: item.Description,
	}
	return r.writeAll(items)
}

func (r *fileRing) Get(key string) (Item, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	items, err := r.readAll()
	if err != nil {
		return Item{}, err
	}
	item, ok := items[key]
	if !ok {
		return Item{}, ErrKeyNotFound
	}
	data, err := base64.StdEncoding.DecodeString(item.Data)
	if err != nil {
		return Item{}, fmt.Errorf("decode item %q: %w", key, err)
	}
	return Item{
		Key:         item.Key,
		Data:        data,
		Label:       item.Label,
		Description: item.Description,
	}, nil
}

func (r *fileRing) Remove(key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	items, err := r.readAll()
	if err != nil {
		return err
	}
	delete(items, key)
	return r.writeAll(items)
}

func (r *fileRing) Keys() ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	items, err := r.readAll()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func (r *fileRing) readAll() (map[string]storedItem, error) {
	data, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]storedItem{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read keyring file: %w", err)
	}
	if len(data) == 0 {
		return map[string]storedItem{}, nil
	}

	items := map[string]storedItem{}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("parse keyring file: %w", err)
	}
	return items, nil
}

func (r *fileRing) writeAll(items map[string]storedItem) error {
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal keyring file: %w", err)
	}
	if err := os.WriteFile(r.path, data, 0o600); err != nil {
		return fmt.Errorf("write keyring file: %w", err)
	}
	return nil
}
