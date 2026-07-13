# Non-positive ticker panics

Negative heartbeat and registry intervals reached `time.NewTicker` and panicked. Heartbeat and every registry ticker path now normalize non-positive intervals to their documented defaults.
