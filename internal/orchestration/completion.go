package orchestration

import (
	"fmt"
	"strings"
	"time"
)

// Completion is the answer to "is this task done, and what do I post?".
//
// Ready is false far more often than a naive completion path would allow, and
// each false is a specific, reportable reason: the agent is still working, or
// nobody has said whether a write-up is owed, or a write-up is owed and has not
// been published. None of those are errors, and none of them justify posting
// "done".
type Completion struct {
	Task *Task
	// Ready means the message below is a genuine completion.
	Ready bool
	// Reason explains a Ready=false result, in words safe to show a requester.
	Reason string
	// Message is the Discord-ready text: the completion when Ready, the
	// outstanding question or blocker otherwise.
	Message string
	// AskReadback is set when the thing standing in the way is the readback
	// question and it has not been asked yet.
	AskReadback bool
}

// Complete evaluates the gate and, when everything is satisfied, marks the task
// completed and renders the message.
//
// Calling it early is normal and harmless: it reports what is outstanding and
// changes nothing except, at most, recording that the readback question has now
// been asked.
func (m *Monitor) Complete(taskID string) (*Completion, error) {
	task, err := m.Store.Get(taskID)
	if err != nil {
		return nil, err
	}
	now := m.now()

	if task.State == StateCompleted {
		// Idempotent: a duplicate completion re-renders the same message
		// rather than posting a second, subtly different one.
		return &Completion{
			Task: task, Ready: true, Message: RenderCompletion(task),
			Reason: "already completed",
		}, nil
	}
	switch task.State {
	case StateAgentLost:
		return &Completion{Task: task, Message: renderLost(task),
			Reason: "the agent is gone; there is nothing to complete"}, nil
	case StateFailed:
		return &Completion{Task: task, Message: renderFailed(task),
			Reason: "the launch failed"}, nil
	}
	if !task.HasWorked {
		return &Completion{Task: task,
			Reason: "the agent has not started working yet"}, nil
	}
	if !isSettled(task.LastStatus) {
		return &Completion{Task: task,
			Reason: fmt.Sprintf("the agent is still %s", orNone(task.LastStatus))}, nil
	}

	switch task.Gate {
	case GateUndecided:
		// Ask once. The question is the product of the gate being undecided,
		// not of anything having gone wrong — and asking must not stop the
		// agent or free the worktree.
		asked := !task.GateQuestionSentAt.IsZero()
		updated, err := m.Store.Update(taskID, func(t *Task) error {
			t.State = StateAwaitingReadbackDecision
			if t.GateQuestionSentAt.IsZero() {
				t.GateQuestionSentAt = now
				t.appendProgress(ProgressEvent{At: now, Kind: "gate",
					Detail: "asked the requester whether a Readback write-up is needed"})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		reason := "waiting for the requester to say whether a Readback write-up is needed"
		if asked {
			reason = "already asked the requester whether a Readback write-up is needed; still waiting"
		}
		return &Completion{
			Task: updated, Reason: reason,
			Message:     GateQuestion(updated),
			AskReadback: !asked,
		}, nil

	case GateRequired:
		if task.Readback.URL == "" {
			updated, err := m.Store.Update(taskID, func(t *Task) error {
				t.State = StateAwaitingReadbackPublish
				return nil
			})
			if err != nil {
				return nil, err
			}
			return &Completion{
				Task:   updated,
				Reason: "this task owes a Readback write-up and none has been published yet",
				Message: fmt.Sprintf(
					"**%s** finished on branch `%s`, but it owes a Readback write-up and none is published yet.\n"+
						"Publish it with `readback push <file> --slug %s`, then record the printed URL.",
					updated.ID, updated.Worktree.Branch, readbackSlug(updated)),
			}, nil
		}
	}

	completed, err := m.Store.Update(taskID, func(t *Task) error {
		t.State = StateCompleted
		t.CompletedAt = now
		t.Detail = "completed"
		t.appendProgress(ProgressEvent{At: now, Kind: "completed",
			Status: t.LastStatus, StateChangeSeq: t.LastSeq,
			Detail: "task reported complete"})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Completion{Task: completed, Ready: true, Message: RenderCompletion(completed)}, nil
}

// readbackSlug is the stable slug a task's document should be pushed under.
// Stable matters: re-pushing under the same slug revises the document behind an
// existing link instead of orphaning the marks already made on it.
func readbackSlug(t *Task) string {
	if t.Readback.Slug != "" {
		return t.Readback.Slug
	}
	return "herdr-" + t.ID
}

// RenderCompletion builds the message that goes back to the thread.
//
// Short on purpose. It answers the four things somebody actually asks when a
// task lands — where is the code, what branch, did the tests pass, where is the
// write-up — and nothing else.
func RenderCompletion(t *Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Task complete** — `%s`\n", t.ID)
	fmt.Fprintf(&b, "project `%s` · branch `%s` · worktree `%s`\n",
		t.Project, orNone(t.Worktree.Branch), orNone(t.Worktree.Path))
	fmt.Fprintf(&b, "tests: %s\n", renderTests(t))
	switch {
	case t.Readback.URL != "":
		fmt.Fprintf(&b, "Report: %s", t.Readback.URL)
		if t.Readback.Slug != "" {
			fmt.Fprintf(&b, " (slug `%s`)", t.Readback.Slug)
		}
		b.WriteString("\n")
	case t.Gate == GateNotNeeded:
		b.WriteString("Readback: not needed for this task\n")
	}
	if s := strings.TrimSpace(t.Summary); s != "" {
		b.WriteString(s)
		if !strings.HasSuffix(s, "\n") {
			b.WriteString("\n")
		}
	}
	if d := elapsed(t); d != "" {
		fmt.Fprintf(&b, "_ran for %s_\n", d)
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderTests(t *Task) string {
	label := string(t.Tests)
	if t.Tests == TestsUnknown {
		label = "unknown (the agent did not report a test run)"
	}
	if d := strings.TrimSpace(t.TestDetail); d != "" {
		return label + " — " + d
	}
	return label
}

func renderLost(t *Task) string {
	return fmt.Sprintf(
		"**%s** lost its agent — %s\n"+
			"The worktree `%s` on branch `%s` is untouched; nothing was relaunched.",
		t.ID, orNone(t.Detail), orNone(t.Worktree.Path), orNone(t.Worktree.Branch))
}

func renderFailed(t *Task) string {
	return fmt.Sprintf("**%s** failed to launch — %s", t.ID, orNone(t.Detail))
}

func elapsed(t *Task) string {
	if t.CreatedAt.IsZero() || t.CompletedAt.IsZero() {
		return ""
	}
	d := t.CompletedAt.Sub(t.CreatedAt)
	if d < time.Second {
		return ""
	}
	d = d.Round(time.Second)
	if d >= time.Hour {
		return d.Round(time.Minute).String()
	}
	return d.String()
}
