package mailstore

import (
	"sync"
	"time"

	"tempmail/internal/config"
)

// Mail holds parsed email data
type Mail struct {
	To          string       `json:"to"`
	From        string       `json:"from"`
	Subject     string       `json:"subject"`
	Text        string       `json:"text"`
	HTML        string       `json:"html"`
	Date        time.Time    `json:"date"`
	Attachments []Attachment `json:"attachments"`
	TS          int64        `json:"_ts"`
}

type Attachment struct {
	Filename    string `json:"filename"`
	Size        int    `json:"size"`
	ContentType string `json:"contentType"`
}

var (
	mu    sync.RWMutex
	store = make(map[string][]*Mail) // mailboxAddr -> mails (newest first)
)

func expireMs() int64 {
	return int64(config.Get().Snap().MailExpireMinutes) * 60 * 1000
}

func maxMails() int {
	return config.Get().Snap().MaxMails
}

func isAlive(m *Mail) bool {
	return time.Now().UnixMilli()-m.TS < expireMs()
}

// Save stores a mail for the given mailbox address.
func Save(addr string, mail *Mail) {
	mail.TS = time.Now().UnixMilli()
	mu.Lock()
	defer mu.Unlock()
	arr := store[addr]
	arr = append([]*Mail{mail}, arr...) // prepend (newest first)
	max := maxMails()
	if len(arr) > max {
		arr = arr[:max]
	}
	store[addr] = arr
}

// GetAll returns non-expired mails for an address (and lazily prunes expired).
func GetAll(addr string) []*Mail {
	mu.Lock()
	defer mu.Unlock()
	arr := store[addr]
	if len(arr) == 0 {
		return nil
	}
	alive := make([]*Mail, 0, len(arr))
	for _, m := range arr {
		if isAlive(m) {
			alive = append(alive, m)
		}
	}
	if len(alive) != len(arr) {
		store[addr] = alive
	}
	return alive
}

// GetByIdx returns the mail at index idx if it exists and is not expired.
func GetByIdx(addr string, idx int) *Mail {
	mu.RLock()
	defer mu.RUnlock()
	arr := store[addr]
	if idx < 0 || idx >= len(arr) {
		return nil
	}
	m := arr[idx]
	if isAlive(m) {
		return m
	}
	return nil
}

// Delete removes the mail at index idx. Returns false if not found.
func Delete(addr string, idx int) bool {
	mu.Lock()
	defer mu.Unlock()
	arr := store[addr]
	if idx < 0 || idx >= len(arr) {
		return false
	}
	store[addr] = append(arr[:idx], arr[idx+1:]...)
	if len(store[addr]) == 0 {
		delete(store, addr)
	}
	return true
}

// StartCleanup runs a background goroutine to purge expired mails every minute.
func StartCleanup() {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for range t.C {
			// Get all addresses first without holding lock for entire cleanup
			mu.Lock()
			addrs := make([]string, 0, len(store))
			for addr := range store {
				addrs = append(addrs, addr)
			}
			mu.Unlock()
			
			// Cleanup each address individually to minimize lock hold time
			now := time.Now().UnixMilli()
			exp := expireMs()
			for _, addr := range addrs {
				mu.Lock()
				arr := store[addr]
				alive := arr[:0]
				for _, m := range arr {
					if now-m.TS < exp {
						alive = append(alive, m)
					}
				}
				if len(alive) == 0 {
					delete(store, addr)
				} else {
					store[addr] = alive
				}
				mu.Unlock()
			}
		}
	}()
}
