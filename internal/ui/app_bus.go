package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/bus"
)

func (a *App) bridge() {
	// Subscribe synchronously so that any Publish issued after New()
	// returns is guaranteed to find a live subscriber.
	subDelta := bus.Subscribe[bus.AssistantTextDelta](a.opts.Bus, bus.SubscribeOptions{BufferSize: 256})
	subThink := bus.Subscribe[bus.AssistantThinkingDelta](a.opts.Bus, bus.SubscribeOptions{BufferSize: 256})
	subAppended := bus.Subscribe[bus.MessageAppended](a.opts.Bus, bus.SubscribeOptions{BufferSize: 128})
	subToolReq := bus.Subscribe[bus.ToolCallRequested](a.opts.Bus, bus.SubscribeOptions{BufferSize: 64})
	subToolProgress := bus.Subscribe[bus.ToolCallProgress](a.opts.Bus, bus.SubscribeOptions{BufferSize: 256})
	subToolDone := bus.Subscribe[bus.ToolCallCompleted](a.opts.Bus, bus.SubscribeOptions{BufferSize: 64})
	subCost := bus.Subscribe[bus.CostUpdated](a.opts.Bus, bus.SubscribeOptions{BufferSize: 64})
	subCtx := bus.Subscribe[bus.ContextUsageUpdated](a.opts.Bus, bus.SubscribeOptions{BufferSize: 64})
	subPerm := bus.Subscribe[bus.PermissionAsked](a.opts.Bus, bus.SubscribeOptions{BufferSize: 32})
	subPermReplied := bus.Subscribe[bus.PermissionReplied](a.opts.Bus, bus.SubscribeOptions{BufferSize: 32})
	subQuestion := bus.Subscribe[bus.QuestionAsked](a.opts.Bus, bus.SubscribeOptions{BufferSize: 16})
	subQuestionAnswered := bus.Subscribe[bus.QuestionAnswered](a.opts.Bus, bus.SubscribeOptions{BufferSize: 16})
	subMCPStatus := bus.Subscribe[bus.MCPStatusUpdated](a.opts.Bus, bus.SubscribeOptions{BufferSize: 32})
	subSubStart := bus.Subscribe[bus.SubagentStarted](a.opts.Bus, bus.SubscribeOptions{BufferSize: 16})
	subSubDone := bus.Subscribe[bus.SubagentCompleted](a.opts.Bus, bus.SubscribeOptions{BufferSize: 16})
	subCmpReq := bus.Subscribe[bus.CompactionRequested](a.opts.Bus, bus.SubscribeOptions{BufferSize: 8})
	subCmpStart := bus.Subscribe[bus.CompactionStarted](a.opts.Bus, bus.SubscribeOptions{BufferSize: 8})
	subCmpDone := bus.Subscribe[bus.CompactionCompleted](a.opts.Bus, bus.SubscribeOptions{BufferSize: 8})
	subCmpFail := bus.Subscribe[bus.CompactionFailed](a.opts.Bus, bus.SubscribeOptions{BufferSize: 8})
	subQueueChanged := bus.Subscribe[bus.QueueChanged](a.opts.Bus, bus.SubscribeOptions{BufferSize: 32})
	subTodoChanged := bus.Subscribe[bus.TodoChanged](a.opts.Bus, bus.SubscribeOptions{BufferSize: 32})
	subTurnStarted := bus.Subscribe[bus.TurnStarted](a.opts.Bus, bus.SubscribeOptions{BufferSize: 16})
	subTurnDone := bus.Subscribe[bus.TurnCompleted](a.opts.Bus, bus.SubscribeOptions{BufferSize: 16})
	subTitle := bus.Subscribe[bus.SessionTitleUpdated](a.opts.Bus, bus.SubscribeOptions{BufferSize: 16})

	stop := a.ctx.Done()

	// One forwarder goroutine per type.  The body is identical in shape;
	// generics over the channel element type would be cleaner but Go
	// closures cannot capture generic type parameters, so each call is
	// type-instantiated explicitly.
	go forward(subDelta.C(), a.busCh, stop, subDelta.Unsubscribe)
	go forward(subThink.C(), a.busCh, stop, subThink.Unsubscribe)
	go forward(subAppended.C(), a.busCh, stop, subAppended.Unsubscribe)
	go forward(subToolReq.C(), a.busCh, stop, subToolReq.Unsubscribe)
	go forward(subToolProgress.C(), a.busCh, stop, subToolProgress.Unsubscribe)
	go forward(subToolDone.C(), a.busCh, stop, subToolDone.Unsubscribe)
	go forward(subCost.C(), a.busCh, stop, subCost.Unsubscribe)
	go forward(subCtx.C(), a.busCh, stop, subCtx.Unsubscribe)
	go forward(subPerm.C(), a.busCh, stop, subPerm.Unsubscribe)
	go forward(subPermReplied.C(), a.busCh, stop, subPermReplied.Unsubscribe)
	go forward(subQuestion.C(), a.busCh, stop, subQuestion.Unsubscribe)
	go forward(subQuestionAnswered.C(), a.busCh, stop, subQuestionAnswered.Unsubscribe)
	go forward(subMCPStatus.C(), a.busCh, stop, subMCPStatus.Unsubscribe)
	go forward(subSubStart.C(), a.busCh, stop, subSubStart.Unsubscribe)
	go forward(subSubDone.C(), a.busCh, stop, subSubDone.Unsubscribe)
	go forward(subCmpReq.C(), a.busCh, stop, subCmpReq.Unsubscribe)
	go forward(subCmpStart.C(), a.busCh, stop, subCmpStart.Unsubscribe)
	go forward(subCmpDone.C(), a.busCh, stop, subCmpDone.Unsubscribe)
	go forward(subCmpFail.C(), a.busCh, stop, subCmpFail.Unsubscribe)
	go forward(subQueueChanged.C(), a.busCh, stop, subQueueChanged.Unsubscribe)
	go forward(subTodoChanged.C(), a.busCh, stop, subTodoChanged.Unsubscribe)
	go forward(subTurnStarted.C(), a.busCh, stop, subTurnStarted.Unsubscribe)
	go forward(subTurnDone.C(), a.busCh, stop, subTurnDone.Unsubscribe)
	go forward(subTitle.C(), a.busCh, stop, subTitle.Unsubscribe)
}

// forward pumps a single typed subscription channel into the shared any
// channel until either source is exhausted or the App context is cancelled.
func forward[T any](in <-chan T, out chan<- any, stop <-chan struct{}, unsubscribe func()) {
	defer unsubscribe()
	for {
		select {
		case ev, ok := <-in:
			if !ok {
				return
			}
			select {
			case out <- ev:
			case <-stop:
				return
			}
		case <-stop:
			return
		}
	}
}

// listenBus is the bubbletea Cmd that reads ONE event off the bridge channel
// and wraps it in a busDelivery.  Update re-issues this Cmd on every
// delivery, creating an infinite read-loop inside the bubbletea machinery.
func (a *App) listenBus() tea.Cmd {
	return func() tea.Msg {
		select {
		case ev, ok := <-a.busCh:
			if !ok {
				return nil
			}
			return busDelivery{Event: ev}
		case <-a.ctx.Done():
			return nil
		}
	}
}

// Handle delivers a single bus event synchronously, exactly as if it had
