package hub

// compositeHub starts and manages multiple ingestion hubs while exposing a
// single Hub interface to the rest of the application.
type compositeHub struct {
	primary     Hub
	hubs        []Hub
	onPublish   func(streamPath, token, remoteAddr string) error
	onUnpublish func(streamPath string)
}

func NewCompositeHub(primary Hub, hubs ...Hub) Hub {
	filtered := make([]Hub, 0, len(hubs))
	seen := make(map[Hub]struct{}, len(hubs))
	for _, h := range hubs {
		if h == nil {
			continue
		}
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		filtered = append(filtered, h)
	}
	if primary == nil && len(filtered) > 0 {
		primary = filtered[0]
	}
	return &compositeHub{primary: primary, hubs: filtered}
}

func (h *compositeHub) Start() error {
	started := make([]Hub, 0, len(h.hubs))
	for _, hub := range h.hubs {
		if err := hub.Start(); err != nil {
			for i := len(started) - 1; i >= 0; i-- {
				started[i].Stop()
			}
			return err
		}
		started = append(started, hub)
	}
	return nil
}

func (h *compositeHub) Stop() {
	for i := len(h.hubs) - 1; i >= 0; i-- {
		h.hubs[i].Stop()
	}
}

func (h *compositeHub) Addr() string {
	if h.primary == nil {
		return ""
	}
	return h.primary.Addr()
}

func (h *compositeHub) SetOnPublish(handler func(streamPath, token, remoteAddr string) error) {
	h.onPublish = handler
	for _, hub := range h.hubs {
		hub.SetOnPublish(handler)
	}
}

func (h *compositeHub) SetOnUnpublish(handler func(streamPath string)) {
	h.onUnpublish = handler
	for _, hub := range h.hubs {
		hub.SetOnUnpublish(handler)
	}
}

func (h *compositeHub) EvictStream(streamPath string) {
	for _, hub := range h.hubs {
		hub.EvictStream(streamPath)
	}
}
