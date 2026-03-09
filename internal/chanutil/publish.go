package chanutil

// PublishLatest sends value to ch, replacing any stale buffered value.
// All operations are non-blocking.
func PublishLatest[T any](ch chan T, value T) {
	select {
	case ch <- value:
	default:
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- value:
		default:
		}
	}
}
