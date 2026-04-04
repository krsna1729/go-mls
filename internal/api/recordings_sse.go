package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type recordingsBroker struct {
	mu       sync.Mutex
	clients  map[chan string]struct{}
	shutdown chan struct{}
	once     sync.Once
}

func newRecordingsBroker() *recordingsBroker {
	return &recordingsBroker{
		clients:  make(map[chan string]struct{}),
		shutdown: make(chan struct{}),
	}
}

func (b *recordingsBroker) Subscribe() (<-chan string, func()) {
	ch := make(chan string, 4)
	b.mu.Lock()
	select {
	case <-b.shutdown:
		close(ch)
	default:
		b.clients[ch] = struct{}{}
	}
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		if _, ok := b.clients[ch]; ok {
			delete(b.clients, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
	return ch, unsubscribe
}

func (b *recordingsBroker) Broadcast(msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for ch := range b.clients {
		select {
		case ch <- msg:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- msg:
			default:
			}
		}
	}
}

func (b *recordingsBroker) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ch, unsubscribe := b.Subscribe()
		defer unsubscribe()

		fmt.Fprint(w, "retry: 3000\n\n")
		flusher.Flush()

		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
			case <-r.Context().Done():
				return
			case <-b.shutdown:
				return
			}
		}
	}
}

func (b *recordingsBroker) Shutdown() {
	b.once.Do(func() {
		close(b.shutdown)
		b.mu.Lock()
		defer b.mu.Unlock()
		for ch := range b.clients {
			close(ch)
		}
		b.clients = make(map[chan string]struct{})
	})
}

type recordingsWatcher struct {
	dir     string
	broker  *recordingsBroker
	logf    func(msg string, args ...interface{})
	watcher *fsnotify.Watcher
	done    chan struct{}
	wg      sync.WaitGroup
}

func newRecordingsWatcher(dir string, broker *recordingsBroker, logf func(msg string, args ...interface{})) (*recordingsWatcher, error) {
	if dir == "" {
		return nil, nil
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	rw := &recordingsWatcher{
		dir:     dir,
		broker:  broker,
		logf:    logf,
		watcher: w,
		done:    make(chan struct{}),
	}
	if err := rw.addExistingDirs(); err != nil {
		w.Close()
		return nil, err
	}

	rw.wg.Add(1)
	go rw.run()
	return rw, nil
}

func (rw *recordingsWatcher) addExistingDirs() error {
	return filepath.Walk(rw.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		return rw.watcher.Add(path)
	})
}

func (rw *recordingsWatcher) run() {
	defer rw.wg.Done()

	for {
		select {
		case event, ok := <-rw.watcher.Events:
			if !ok {
				return
			}

			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}

			if info, err := os.Stat(event.Name); err == nil && info.IsDir() && event.Op&fsnotify.Create != 0 {
				_ = rw.watcher.Add(event.Name)
			}

			if rw.logf != nil {
				rw.logf("recordings watcher event %s %s", event.Op.String(), event.Name)
			}
			rw.broker.Broadcast("update")

		case err, ok := <-rw.watcher.Errors:
			if !ok {
				return
			}
			if rw.logf != nil {
				rw.logf("recordings watcher error: %v", err)
			}
		case <-rw.done:
			return
		}
	}
}

func (rw *recordingsWatcher) Shutdown() {
	if rw == nil {
		return
	}
	close(rw.done)
	_ = rw.watcher.Close()
	rw.wg.Wait()
}
