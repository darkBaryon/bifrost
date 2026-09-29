// 本文件验证登录比较与账号哈希确实共用并发槽，并在故障后释放配额。
package auth

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type blockingHasher struct {
	entered chan struct{}
	release chan struct{}
}

func (h blockingHasher) wait() {
	h.entered <- struct{}{}
	<-h.release
}

func (h blockingHasher) Hash(string) (string, error)          { h.wait(); return "hash", nil }
func (h blockingHasher) Compare(string, string) (bool, error) { h.wait(); return true, nil }
func (blockingHasher) ValidHash(string) bool                  { return true }

func TestPasswordsShareHashAndCompareLimit(t *testing.T) {
	hasher := blockingHasher{entered: make(chan struct{}, maxConcurrentHashes), release: make(chan struct{})}
	passwords := NewPasswords(hasher)
	var workers sync.WaitGroup
	for i := 0; i < maxConcurrentHashes; i++ {
		workers.Add(1)
		go func(hash bool) {
			defer workers.Done()
			var err error
			if hash {
				_, err = passwords.Hash("secret")
			} else {
				_, err = passwords.Compare("hash", "secret")
			}
			if err != nil {
				t.Errorf("reserved slot failed: %v", err)
			}
		}(i%2 == 0)
	}
	// 失败断言也必须释放等待中的工作，避免测试留下阻塞 goroutine。
	defer func() { close(hasher.release); workers.Wait() }()
	for i := 0; i < maxConcurrentHashes; i++ {
		select {
		case <-hasher.entered:
		case <-time.After(time.Second):
			t.Fatal("password computations did not enter all slots")
		}
	}
	if _, err := passwords.Hash("secret"); err != ErrLimited {
		t.Fatalf("fifth hash did not fail immediately: %v", err)
	}
	if _, err := passwords.Compare("hash", "secret"); err != ErrLimited {
		t.Fatalf("fifth compare used another limit: %v", err)
	}
	if !passwords.ValidHash("hash") {
		t.Fatal("format check should not consume computation slots")
	}
}

type failingHasher struct{}

func (failingHasher) Hash(string) (string, error) { return "", errors.New("private hash error") }
func (failingHasher) Compare(string, string) (bool, error) {
	return false, errors.New("private compare error")
}
func (failingHasher) ValidHash(string) bool { return false }

func TestPasswordsReleaseSlotsAfterFailure(t *testing.T) {
	passwords := NewPasswords(failingHasher{})
	for i := 0; i < 2*maxConcurrentHashes; i++ {
		if _, err := passwords.Hash("secret"); err != ErrUnavailable {
			t.Fatalf("hash slot leaked or error disclosed: %v", err)
		}
		if _, err := passwords.Compare("hash", "secret"); err != ErrUnavailable {
			t.Fatalf("compare slot leaked or error disclosed: %v", err)
		}
	}
	for _, unavailable := range []*Passwords{nil, NewPasswords(nil)} {
		if _, err := unavailable.Hash("secret"); err != ErrUnavailable || unavailable.ValidHash("hash") {
			t.Fatal("missing hasher accepted")
		}
	}
}
