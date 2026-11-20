package pipe

import (
	"bufio"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

// echo serves every connection on l by writing back each line it reads, until l is closed.
func echo(t *testing.T, l Listener) *sync.WaitGroup {
	t.Helper()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadBytes('\n')
					if err != nil {
						return
					}
					if _, err := c.Write(line); err != nil {
						return
					}
				}
			}()
		}
	}()
	return &wg
}

func listen(t *testing.T) (string, Listener) {
	t.Helper()
	addr := testAddress(t)
	l, err := Listen(addr)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	return addr, l
}

func roundTrip(t *testing.T, c io.ReadWriter, r *bufio.Reader, msg string) {
	t.Helper()
	if _, err := io.WriteString(c, msg+"\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	done := make(chan struct{})
	var line string
	var err error
	go func() { line, err = r.ReadString('\n'); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("no reply within 5 s")
	}
	if err != nil || line != msg+"\n" {
		t.Fatalf("reply %q, err %v; want %q", line, err, msg)
	}
}

func TestBytesFlowBothWays(t *testing.T) {
	addr, l := listen(t)
	wg := echo(t, l)
	defer func() { l.Close(); wg.Wait() }()
	c, err := Dial(addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	r := bufio.NewReader(c)
	roundTrip(t, c, r, "hello")
	roundTrip(t, c, r, "second line")
}

func TestManyConcurrentClients(t *testing.T) {
	addr, l := listen(t)
	wg := echo(t, l)
	defer func() { l.Close(); wg.Wait() }()
	var cw sync.WaitGroup
	for i := 0; i < 16; i++ {
		cw.Add(1)
		go func(i int) {
			defer cw.Done()
			c, err := Dial(addr)
			if err != nil {
				t.Errorf("client %d: Dial: %v", i, err)
				return
			}
			defer c.Close()
			r := bufio.NewReader(c)
			for j := 0; j < 5; j++ {
				msg := fmt.Sprintf("client %d message %d", i, j)
				if _, err := io.WriteString(c, msg+"\n"); err != nil {
					t.Errorf("client %d: write: %v", i, err)
					return
				}
				line, err := r.ReadString('\n')
				if err != nil || line != msg+"\n" {
					t.Errorf("client %d: reply %q, err %v", i, line, err)
					return
				}
			}
		}(i)
	}
	cw.Wait()
}

func TestSecondListenerOnTheSameAddressIsRefused(t *testing.T) {
	skipIfNoSquatProtection(t)
	addr, l := listen(t)
	defer l.Close()
	if l2, err := Listen(addr); err == nil {
		l2.Close()
		t.Fatal("a second listener on a live address succeeded; a squatter could take over the endpoint")
	}
}

func TestCloseFreesTheAddress(t *testing.T) {
	addr, l := listen(t)
	wg := echo(t, l)
	c, err := Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(c)
	roundTrip(t, c, r, "ping")
	c.Close()
	l.Close()
	wg.Wait()
	l2, err := Listen(addr)
	if err != nil {
		t.Fatalf("address not released: %v", err)
	}
	l2.Close()
}
