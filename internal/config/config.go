package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type Config struct {
	Port              int      `json:"PORT"`
	SMTPPort          int      `json:"SMTP_PORT"`
	SMTPHost          string   `json:"SMTP_HOST"`
	MaxMails          int      `json:"MAX_MAILS"`
	MailExpireMinutes int      `json:"MAIL_EXPIRE_MINUTES"`
	AdminUser         string   `json:"ADMIN_USER"`
	AdminPassword     string   `json:"ADMIN_PASSWORD"`
	AdminGroup        string   `json:"ADMIN_GROUP"` // OIDC 模式：有此 group 的用户可访问 admin
	ForbiddenPrefixes []string `json:"FORBIDDEN_PREFIXES"`
	SessionSecret     string   `json:"SESSION_SECRET"`
	BaseURL           string   `json:"BASE_URL"`

	mu sync.RWMutex
}

var instance *Config
var once sync.Once

func Load(path string) *Config {
	once.Do(func() {
		c := &Config{
			Port:              80,
			SMTPPort:          25,
			SMTPHost:          "0.0.0.0",
			MaxMails:          50,
			MailExpireMinutes: 10,
			AdminUser:         "admin",
			AdminPassword:     "password",
			ForbiddenPrefixes: []string{"admin", "root", "support", "test"},
			SessionSecret:     "change_me_in_production",
		}
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, c)
		}
		// env overrides
		if v := os.Getenv("PORT"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				c.Port = n
			}
		}
		if v := os.Getenv("SMTP_PORT"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				c.SMTPPort = n
			}
		}
		if v := os.Getenv("SMTP_HOST"); v != "" {
			c.SMTPHost = v
		}
		if v := os.Getenv("MAX_MAILS"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				c.MaxMails = n
			}
		}
		if v := os.Getenv("MAIL_EXPIRE_MINUTES"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				c.MailExpireMinutes = n
			}
		}
		if v := os.Getenv("ADMIN_USER"); v != "" {
			c.AdminUser = v
		}
		if v := os.Getenv("ADMIN_PASSWORD"); v != "" {
			c.AdminPassword = v
		}
		if v := os.Getenv("ADMIN_GROUP"); v != "" {
			c.AdminGroup = v
		}
		if v := os.Getenv("FORBIDDEN_PREFIXES"); v != "" {
			c.ForbiddenPrefixes = splitPrefixes(v)
		}
		if v := os.Getenv("SESSION_SECRET"); v != "" {
			c.SessionSecret = v
		}
		if v := os.Getenv("BASE_URL"); v != "" {
			c.BaseURL = v
		}
		instance = c
	})
	return instance
}

func Get() *Config {
	return instance
}

func splitPrefixes(s string) []string {
	// Try JSON array first
	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err == nil {
		return arr
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			result = append(result, t)
		}
	}
	return result
}

func (c *Config) GetDomain() string {
	c.mu.RLock()
	baseURL := c.BaseURL
	c.mu.RUnlock()

	if baseURL != "" {
		u := baseURL
		if idx := strings.Index(u, "://"); idx >= 0 {
			u = u[idx+3:]
		}
		if idx := strings.Index(u, "/"); idx >= 0 {
			u = u[:idx]
		}
		return u
	}
	return "localhost"
}

func (c *Config) IsForbidden(prefix string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p := strings.ToLower(prefix)
	for _, f := range c.ForbiddenPrefixes {
		if strings.ToLower(f) == p {
			return true
		}
	}
	return false
}

// Snapshot returns a consistent copy of mutable config fields for safe concurrent reads.
type Snapshot struct {
	MaxMails          int
	MailExpireMinutes int
	ForbiddenPrefixes []string
	AdminUser         string
	AdminPassword     string
}

func (c *Config) Snap() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	fp := make([]string, len(c.ForbiddenPrefixes))
	copy(fp, c.ForbiddenPrefixes)
	return Snapshot{
		MaxMails:          c.MaxMails,
		MailExpireMinutes: c.MailExpireMinutes,
		ForbiddenPrefixes: fp,
		AdminUser:         c.AdminUser,
		AdminPassword:     c.AdminPassword,
	}
}

func (c *Config) Update(maxMails, expireMin int, forbidden []string, adminUser, adminPassword string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.MaxMails = maxMails
	c.MailExpireMinutes = expireMin
	c.ForbiddenPrefixes = forbidden
	c.AdminUser = adminUser
	if adminPassword != "" {
		c.AdminPassword = adminPassword
	}
	return c.save()
}

func (c *Config) save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// Try to write next to the executable first, fall back to cwd
	var configPath string
	if exe, err := os.Executable(); err == nil {
		configPath = filepath.Join(filepath.Dir(exe), "config.json")
	} else {
		configPath = "config.json"
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}
