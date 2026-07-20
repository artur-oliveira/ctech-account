package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/valkey-io/valkey-go"
)

var ErrNotFound = errors.New("key not found")

// valkeyDB isolates this service's keys from other consumers of the shared
// Valkey instance (ctech-wallet, ctech-dfe, ...). Selected on every connection
// via ClientOption.SelectDB so isolation holds regardless of VALKEY_URL.
const valkeyDB = 3

type memEntry struct {
	data    []byte
	expires time.Time
}

type Client struct {
	client   valkey.Client
	enabled  bool
	inMemory bool
	mu       sync.RWMutex
	mem      map[string]memEntry
}

// NewInMemory returns an in-memory cache client suitable for testing.
func NewInMemory() *Client {
	return &Client{enabled: true, inMemory: true, mem: make(map[string]memEntry)}
}

func New(url string) (*Client, error) {
	if url == "" {
		slog.Warn("valkey url is null")
		return &Client{enabled: false}, nil
	}

	opts, err := valkey.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parsing valkey URL: %w", err)
	}
	opts.SelectDB = valkeyDB

	client, err := valkey.NewClient(opts)
	if err != nil {
		return nil, fmt.Errorf("creating valkey client: %w", err)
	}

	return &Client{client: client, enabled: true}, nil
}

func (c *Client) Enabled() bool {
	return c.enabled
}

func (c *Client) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	if !c.enabled {
		return nil
	}

	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshaling value: %w", err)
	}

	if c.inMemory {
		c.mu.Lock()
		c.mem[key] = memEntry{data: data, expires: time.Now().Add(ttl)}
		c.mu.Unlock()
		return nil
	}

	cmd := c.client.B().Set().Key(key).Value(string(data)).Ex(ttl).Build()
	return c.client.Do(ctx, cmd).Error()
}

func (c *Client) Get(ctx context.Context, key string, dest any) error {
	if !c.enabled {
		return ErrNotFound
	}

	if c.inMemory {
		c.mu.RLock()
		entry, ok := c.mem[key]
		c.mu.RUnlock()
		if !ok || time.Now().After(entry.expires) {
			return ErrNotFound
		}
		return json.Unmarshal(entry.data, dest)
	}

	cmd := c.client.B().Get().Key(key).Build()
	result := c.client.Do(ctx, cmd)

	if result.Error() != nil {
		if valkey.IsValkeyNil(result.Error()) {
			return ErrNotFound
		}
		return fmt.Errorf("valkey GET: %w", result.Error())
	}

	str, err := result.ToString()
	if err != nil {
		return fmt.Errorf("reading result: %w", err)
	}

	if err := json.Unmarshal([]byte(str), dest); err != nil {
		return fmt.Errorf("unmarshaling value: %w", err)
	}
	return nil
}

// GetDel atomically reads a key and deletes it in a single operation, so a
// value can be consumed exactly once even under concurrent requests. Returns
// ErrNotFound if the key is absent (or already consumed by a racing caller).
func (c *Client) GetDel(ctx context.Context, key string, dest any) error {
	if !c.enabled {
		return ErrNotFound
	}

	if c.inMemory {
		c.mu.Lock()
		entry, ok := c.mem[key]
		if ok {
			delete(c.mem, key)
		}
		c.mu.Unlock()
		if !ok || time.Now().After(entry.expires) {
			return ErrNotFound
		}
		return json.Unmarshal(entry.data, dest)
	}

	cmd := c.client.B().Getdel().Key(key).Build()
	result := c.client.Do(ctx, cmd)

	if result.Error() != nil {
		if valkey.IsValkeyNil(result.Error()) {
			return ErrNotFound
		}
		return fmt.Errorf("valkey GETDEL: %w", result.Error())
	}

	str, err := result.ToString()
	if err != nil {
		return fmt.Errorf("reading result: %w", err)
	}

	if err := json.Unmarshal([]byte(str), dest); err != nil {
		return fmt.Errorf("unmarshaling value: %w", err)
	}
	return nil
}

func (c *Client) Delete(ctx context.Context, key string) error {
	if !c.enabled {
		return nil
	}
	if c.inMemory {
		c.mu.Lock()
		delete(c.mem, key)
		c.mu.Unlock()
		return nil
	}
	cmd := c.client.B().Del().Key(key).Build()
	return c.client.Do(ctx, cmd).Error()
}

func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	if !c.enabled {
		return false, nil
	}
	cmd := c.client.B().Exists().Key(key).Build()
	n, err := c.client.Do(ctx, cmd).AsInt64()
	if err != nil {
		return false, fmt.Errorf("valkey EXISTS: %w", err)
	}
	return n > 0, nil
}

// Count returns the current integer value of a counter, or 0 if the key is
// absent or expired. Used by the rate limiter to read a window without bumping it.
func (c *Client) Count(ctx context.Context, key string) (int64, error) {
	if !c.enabled {
		return 0, nil
	}

	if c.inMemory {
		c.mu.RLock()
		entry, ok := c.mem[key]
		c.mu.RUnlock()
		if !ok || time.Now().After(entry.expires) {
			return 0, nil
		}
		var n int64
		_ = json.Unmarshal(entry.data, &n)
		return n, nil
	}

	cmd := c.client.B().Get().Key(key).Build()
	result := c.client.Do(ctx, cmd)
	if result.Error() != nil {
		if valkey.IsValkeyNil(result.Error()) {
			return 0, nil
		}
		return 0, fmt.Errorf("valkey GET: %w", result.Error())
	}
	n, err := result.AsInt64()
	if err != nil {
		return 0, fmt.Errorf("reading counter: %w", err)
	}
	return n, nil
}

// Incr atomically increments a counter and sets TTL if it's a new key. Used for rate limiting.
func (c *Client) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	if !c.enabled {
		return 0, nil
	}

	if c.inMemory {
		c.mu.Lock()
		defer c.mu.Unlock()
		entry, ok := c.mem[key]
		var n int64
		fresh := !ok || time.Now().After(entry.expires)
		if !fresh {
			_ = json.Unmarshal(entry.data, &n)
		}
		n++
		data, _ := json.Marshal(n)
		expires := entry.expires
		if fresh {
			expires = time.Now().Add(ttl) // reset window only on a new key (NX semantics)
		}
		c.mem[key] = memEntry{data: data, expires: expires}
		return n, nil
	}

	// Atomic increment with a guaranteed TTL. SET ... NX EX only touches a
	// brand-new key, so it seeds the expiry without clobbering an existing
	// counter's value; INCR then bumps it. This replaces the old INCR-then-EXPIRE
	// pair, which could crash between the two ops and leave a key with no TTL —
	// a permanent lockout for a client that then stops (see CAC-010).
	setCmd := c.client.B().Set().Key(key).Value("0").Nx().Ex(ttl).Build()
	if err := c.client.Do(ctx, setCmd).Error(); err != nil {
		// SET NX is a no-op (not an error) when the key already exists, so any
		// error here is a transport failure worth surfacing so the limiter can
		// fail closed.
		return 0, fmt.Errorf("valkey SET NX: %w", err)
	}

	incrCmd := c.client.B().Incr().Key(key).Build()
	n, err := c.client.Do(ctx, incrCmd).AsInt64()
	if err != nil {
		return 0, fmt.Errorf("valkey INCR: %w", err)
	}

	return n, nil
}

// SetNX sets key to value only when the key does not exist (distributed
// lock). Returns true when the lock was acquired; false when the key exists
// or the cache is disabled.
func (c *Client) SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	if !c.enabled {
		return false, nil
	}

	if c.inMemory {
		c.mu.Lock()
		defer c.mu.Unlock()
		if entry, ok := c.mem[key]; ok && time.Now().Before(entry.expires) {
			return false, nil
		}
		data, _ := json.Marshal(value)
		c.mem[key] = memEntry{data: data, expires: time.Now().Add(ttl)}
		return true, nil
	}

	cmd := c.client.B().Set().Key(key).Value(value).Nx().Ex(ttl).Build()
	result := c.client.Do(ctx, cmd)
	if result.Error() != nil {
		if valkey.IsValkeyNil(result.Error()) {
			return false, nil // key already exists — lock not acquired
		}
		return false, fmt.Errorf("valkey SET NX: %w", result.Error())
	}
	return true, nil
}

// Ping sends a PING command to verify connectivity.
func (c *Client) Ping(ctx context.Context) error {
	if !c.enabled {
		return nil
	}
	cmd := c.client.B().Ping().Build()
	return c.client.Do(ctx, cmd).Error()
}

func (c *Client) Close() {
	if c.enabled {
		c.client.Close()
	}
}
