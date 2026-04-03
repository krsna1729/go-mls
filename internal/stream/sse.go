package stream

import (
	"net/http"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type SSEBroker struct {
	clients  map[chan string]struct{}
	mu       sync.Mutex
	shutdown chan struct{}
	once     sync.Once

	watcher  *fsnotify.Watcher
	watchDir string
}

func NewSSEBroker() *SSEBroker {
	return &SSEBroker{
		clients:  make(map[chan string]struct{}),
		shutdown: make(chan struct{}),
	}
}

func NewSSEBrokerWithWatcher(dir string) (*SSEBroker, error) {
	broker := &SSEBroker{
		clients:  make(map[chan string]struct{}),
		shutdown: make(chan struct{}),
		watchDir: dir,
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	broker.watcher = watcher

	if err := watcher.Add(dir); err != nil {
		watcher.Close()
		return nil, err
	}

	go broker.watchEvents()

	return broker, nil
}

func (b *SSEBroker) watchEvents() {
	for {
		select {
		case event, ok := <-b.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) {
				b.refreshWatchDir()
				b.Broadcast("update")
			}
		case err, ok := <-b.watcher.Errors:
			if !ok {
				return
			}
			_ = err
		case <-b.shutdown:
			return
		}
	}
}

func (b *SSEBroker) refreshWatchDir() {
	if b.watcher == nil || b.watchDir == "" {
		return
	}
	b.watcher.Add(b.watchDir)
}

func (b *SSEBroker) Broadcast(msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (b *SSEBroker) Subscribe() (<-chan string, func()) {
	ch := make(chan string, 1)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	cleanup := func() {
		b.mu.Lock()
		delete(b.clients, ch)
		b.mu.Unlock()
	}
	return ch, cleanup
}

func (b *SSEBroker) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		ch, cleanup := b.Subscribe()
		defer cleanup()

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					return
				}
				w.Write([]byte("data: " + msg + "\n\n"))
				flusher.Flush()
			case <-r.Context().Done():
				return
			case <-b.shutdown:
				return
			}
		}
	}
}

func (b *SSEBroker) Shutdown() {
	b.once.Do(func() {
		close(b.shutdown)
		if b.watcher != nil {
			b.watcher.Close()
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		for ch := range b.clients {
			close(ch)
		}
		b.clients = make(map[chan string]struct{})
	})
}
