package proxy

import (
	"log"
	"time"
)

// The live-compact observation worker.
//
// Why a queue: encoding/json re-validates the entire body at every nesting
// level, so parsing an 8 MB Messages request costs ~450 ms across four full
// scans (measured 2026-11-18; a hand-written single-pass scanner still costs
// ~73 ms, so Phase B1 will have to think about inline cost separately). Doing
// that inline would add that latency to every large turn, which Phase A may not
// do (zero behavior change). The request path only gates and enqueues; the
// body slice already owned by the handler is shared with the worker instead of
// copied.
//
// Ordering: each session token has its own FIFO, and a token is claimed by at
// most one worker at a time. Two requests of the same session therefore
// analyze in arrival order (which the drift detector needs), while different
// sessions run concurrently.
const (
	// liveCompactQueueDepth bounds pending work per session. A burst of turns
	// still fits; a runaway producer cannot grow memory without bound.
	liveCompactQueueDepth = 4
	// liveCompactWorkers caps concurrent analyses. The work is CPU-bound and
	// short; more workers only add scheduling noise on a busy node.
	liveCompactWorkers = 2
	// liveCompactRetainBudget caps the total body bytes held by the queue
	// across all sessions. Per-session depth alone is not a bound: a body is
	// tens of megabytes, so a few active sessions would pin hundreds of MB.
	liveCompactRetainBudget = 96 << 20
)

type liveCompactJob struct {
	bytes int
	run   func()
}

func (s *Server) initLiveCompactWorkers() {
	s.workReady = make(chan string, 64)
	s.workStop = make(chan struct{})
	for i := 0; i < liveCompactWorkers; i++ {
		s.workerWG.Add(1)
		go s.liveCompactWorker()
	}
}

// enqueueLiveCompactWork schedules one analysis. It never blocks. When the
// session queue is full, or the node-wide byte budget is exhausted, the new
// observation is dropped and counted: under overload losing the newest turn
// also breaks the drift chain for that session, so drops are reported at close.
func (s *Server) enqueueLiveCompactWork(token string, bodyBytes int, job func()) bool {
	s.workMu.Lock()
	if s.workClosed {
		s.workMu.Unlock()
		return false
	}
	queue := s.workQueues[token]
	if queue == nil {
		queue = make([]liveCompactJob, 0, liveCompactQueueDepth)
	}
	if len(queue) >= liveCompactQueueDepth || s.workRetained+bodyBytes > liveCompactRetainBudget {
		s.workDropped++
		s.workMu.Unlock()
		return false
	}
	wasEmpty := len(queue) == 0
	s.workQueues[token] = append(queue, liveCompactJob{bytes: bodyBytes, run: job})
	s.workRetained += bodyBytes
	s.workMu.Unlock()
	if wasEmpty {
		// Token just became ready. The channel has room for every live token
		// because each token appears at most once (only the empty→non-empty
		// transition emits), so a non-blocking send is safe; guard anyway.
		select {
		case s.workReady <- token:
		default:
			s.workMu.Lock()
			s.workDropped++
			s.workMu.Unlock()
		}
	}
	return true
}

func (s *Server) liveCompactWorker() {
	defer s.workerWG.Done()
	for {
		select {
		case <-s.workStop:
			return
		case token := <-s.workReady:
			for {
				s.workMu.Lock()
				queue := s.workQueues[token]
				if len(queue) == 0 {
					delete(s.workQueues, token)
					s.workMu.Unlock()
					break
				}
				job := queue[0]
				s.workQueues[token] = queue[1:]
				s.workRetained -= job.bytes
				s.workInFlight++
				s.workMu.Unlock()
				job.run()
				s.workMu.Lock()
				s.workInFlight--
				s.workMu.Unlock()
			}
		}
	}
}

// flushLiveCompactWork waits for the queues to drain, with a bounded deadline.
// Tests use it to observe results deterministically; production never does.
func (s *Server) flushLiveCompactWork(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.workMu.Lock()
		pending := s.workInFlight
		for _, queue := range s.workQueues {
			pending += len(queue)
		}
		s.workMu.Unlock()
		if pending == 0 {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// closeLiveCompactWorkers stops accepting work and waits for in-flight
// analyses. Called from Close.
func (s *Server) closeLiveCompactWorkers() {
	s.workMu.Lock()
	if s.workClosed {
		s.workMu.Unlock()
		return
	}
	s.workClosed = true
	dropped := s.workDropped
	s.workMu.Unlock()
	close(s.workStop)
	s.workerWG.Wait()
	if dropped > 0 {
		log.Printf("[live-compact] dropped %d observation(s): queue full", dropped)
	}
}
