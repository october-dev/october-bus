package bus

import "sync"

type signalKey struct {
	scopeID    string
	consumerID string
}

type runtimeSignal struct {
	channel chan struct{}
	waiters int
}

type runtimeSignals struct {
	mu       sync.Mutex
	channels map[signalKey]*runtimeSignal
}

func newRuntimeSignals() *runtimeSignals {
	return &runtimeSignals{channels: make(map[signalKey]*runtimeSignal)}
}

func (signals *runtimeSignals) subscribe(key signalKey) (<-chan struct{}, func()) {
	channel, unsubscribe, _ := signals.subscribeLimited(key, 0)
	return channel, unsubscribe
}

func (signals *runtimeSignals) subscribeLimited(key signalKey, limit int) (<-chan struct{}, func(), bool) {
	signals.mu.Lock()
	signal := signals.channels[key]
	if signal == nil {
		signal = &runtimeSignal{channel: make(chan struct{})}
		signals.channels[key] = signal
	}
	if limit > 0 && signal.waiters >= limit {
		if signal.waiters == 0 {
			delete(signals.channels, key)
		}
		signals.mu.Unlock()
		return nil, func() {}, false
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
	}, true
}

func (signals *runtimeSignals) notify(key signalKey) {
	signals.mu.Lock()
	defer signals.mu.Unlock()
	if signal := signals.channels[key]; signal != nil {
		close(signal.channel)
		delete(signals.channels, key)
	}
}
