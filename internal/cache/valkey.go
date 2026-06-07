package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/valkey-io/valkey-go"
)

var ErrNotFound = errors.New("key not found")

type Client struct {
	client  valkey.Client
	enabled bool
}

func New(url string) (*Client, error) {
	if url == "" {
		return &Client{enabled: false}, nil
	}

	opts, err := valkey.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parsing valkey URL: %w", err)
	}

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

	cmd := c.client.B().Set().Key(key).Value(string(data)).Ex(ttl).Build()
	return c.client.Do(ctx, cmd).Error()
}

func (c *Client) Get(ctx context.Context, key string, dest any) error {
	if !c.enabled {
		return ErrNotFound
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

func (c *Client) Delete(ctx context.Context, key string) error {
	if !c.enabled {
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

// Incr atomically increments a counter and sets TTL if it's a new key. Used for rate limiting.
func (c *Client) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	if !c.enabled {
		return 0, nil
	}

	// Incrementa o contador
	incrCmd := c.client.B().Incr().Key(key).Build()

	n, err := c.client.Do(ctx, incrCmd).AsInt64()
	if err != nil {
		return 0, fmt.Errorf("valkey INCR: %w", err)
	}

	// Define TTL apenas se a chave ainda não tiver expiração
	expireCmd := c.client.B().
		Expire().
		Key(key).
		Seconds(int64(ttl.Seconds())).
		Nx().
		Build()

	if err := c.client.Do(ctx, expireCmd).Error(); err != nil {
		return 0, fmt.Errorf("valkey EXPIRE: %w", err)
	}

	return n, nil
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
