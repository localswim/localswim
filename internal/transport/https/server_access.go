package https

var unavailableServerDone = func() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}()

// Done closes after both serving workers exit. It conveys transport completion,
// not domain outcome or source ownership; composition still owns shutdown order.
func (s *Server) Done() <-chan struct{} {
	if s == nil || s.done == nil {
		return unavailableServerDone
	}
	return s.done
}
