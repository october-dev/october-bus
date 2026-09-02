package bus

import "sync"

type inboxSignalKey struct {
	scopeID string
	agentID string
}

type inboxSignal struct {
	channel chan struct{}
	waiters int
}

type inboxSignals struct {
	mu       sync.Mutex
	channels map[inboxSignalKey]*inboxSignal
}

func newInboxSignals() *inboxSignals {
	return &inboxSignals{channels: make(map[inboxSignalKey]*inboxSignal)}
}

func (signals *inboxSignals) subscribe(key inboxSignalKey) (<-chan struct{}, func()) {
	signals.mu.Lock()
	signal := signals.channels[key]
	if signal == nil {
		signal = &inboxSignal{channel: make(chan struct{})}
		signals.channels[key] = signal
	}
	signal.waiters++
	signals.mu.Unlock()

	var once sync.Once
	return signal.channel, func() {
		once.Do(func() {
			signals.mu.Lock()
			defer signals.mu.Unlock()
			if signals.channels[key] != signal {
				return
			}
			signal.waiters--
			if signal.waiters == 0 {
				delete(signals.channels, key)
			}
		})
	}
}

func (signals *inboxSignals) notify(key inboxSignalKey) {
	signals.mu.Lock()
	defer signals.mu.Unlock()
	if signal := signals.channels[key]; signal != nil {
		close(signal.channel)
		delete(signals.channels, key)
	}
}
