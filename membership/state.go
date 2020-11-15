package membership

func (p *pool) NotifyMsg(b []byte) {
	if p.stateDelegate != nil {
		p.stateDelegate.NotifyMsg(b)
	}
}
func (p *pool) GetBroadcasts(overhead, limit int) [][]byte {
	if p.stateDelegate != nil {
		return p.stateDelegate.GetBroadcasts(overhead, limit)
	}
	return nil
}
func (p *pool) LocalState(join bool) []byte {
	if p.stateDelegate != nil {
		return p.stateDelegate.LocalState(join)
	}
	return nil
}

func (p *pool) MergeRemoteState(buf []byte, join bool) {
	if p.stateDelegate != nil {
		p.stateDelegate.MergeRemoteState(buf, join)
	}
}
