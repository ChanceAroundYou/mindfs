package agent

// RegisterRethreadedThread records a thread that a *running* session just forked
// onto the current model provider (session.rethreadThread), after the -32600
// provider mismatch.
//
// The open path already does both halves of this (RegisterThread +
// MarkProviderSwitch); the in-session path must not be weaker. The fork copies
// history verbatim, including reasoning encrypted by the old provider, which
// the new provider cannot decrypt — without the marker the very next request
// fails with invalid_encrypted_content, i.e. the same conversation dies a second
// way right after being repaired (G-PK).
//
// 单独一个文件是为了不再撑大 pool.go：那份文件在本次改动之前已经 513 行，超过
// 500 行硬线。
func (p *Pool) RegisterRethreadedThread(sessionKey, threadID string) {
	p.RegisterThread(sessionKey, threadID)
	if p.compactProxy == nil {
		return
	}
	p.compactProxy.MarkProviderSwitch(threadID)
}
