package jev

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const typedBody = `{"model":"jev-latest","usage":{},"answers":{` +
	`"urgent":{"type":"noul","noul":0.93},` +
	`"team":{"type":"choice","choice":"technical","confidence":0.82,"probabilities":{"billing":0.18,"technical":0.82}},` +
	`"urgency":{"type":"score","score":1.8,"confidence":0.7,"legend":{"0":"can wait","1":"this week","2":"today"},"probabilities":{"0":0.05,"1":0.1,"2":0.85}}}}`

// hasType compiles only when v is exactly of type T, which turns an inferred
// type into something the test suite checks.
func hasType[T any](T) {}

func TestAskEndToEnd(t *testing.T) {
	t.Parallel()

	client, rec := newTestClient(t, respondJSON(http.StatusOK, typedBody))

	req := SystemOneRequest{State: "Our integration returns 500 on every request."}
	urgent := Ask(&req, "urgent", Noul{Instructions: "Is this urgent?"})
	team := Ask(&req, "team", ChoiceOf[Team]{Instructions: "Which team?", Criteria: LabelsOf(Billing, Technical)})
	urgency := Ask(&req, "urgency", ScoreOf[Urgency]{Instructions: "How urgent?", Criteria: Levels("can wait", "this week", "today")})

	// The answer types are inferred from the questions: these assignments are
	// the compile-time half of the test.
	hasType[Key[NoulAnswer]](urgent)
	hasType[Key[ChoiceAnswerOf[Team]]](team)
	hasType[Key[ScoreAnswerOf[Urgency]]](urgency)

	resp, err := client.SystemOne(t.Context(), req)
	if err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}

	// Ask registered every question on the request that was sent.
	_, body := rec.last()
	for _, name := range []string{"urgent", "team", "urgency"} {
		if !strings.Contains(body, `"`+name+`":{"type"`) {
			t.Errorf("request body is missing question %q: %s", name, body)
		}
	}

	u, err := urgent.Answer(resp)
	if err != nil || u.Noul != 0.93 {
		t.Errorf("urgent.Answer() = %+v, %v", u, err)
	}

	tm, err := team.Answer(resp)
	if err != nil {
		t.Fatalf("team.Answer() error = %v", err)
	}
	if tm.Choice != Technical || tm.Confidence != 0.82 {
		t.Errorf("team.Answer() = %+v, want Technical at 0.82", tm)
	}
	if want := map[Team]float64{Billing: 0.18, Technical: 0.82}; !reflect.DeepEqual(tm.Probabilities, want) {
		t.Errorf("Probabilities = %v, want %v", tm.Probabilities, want)
	}
	if got := tm.Ranked(); !reflect.DeepEqual(got, []Team{Technical, Billing}) {
		t.Errorf("Ranked() = %v", got)
	}

	ug, err := urgency.Answer(resp)
	if err != nil {
		t.Fatalf("urgency.Answer() error = %v", err)
	}
	if ug.Level() != Today || ug.Description() != "today" {
		t.Errorf("Level() = %v, Description() = %v, want Today and today", ug.Level(), ug.Description())
	}
	if ug.Legend[CanWait] != "can wait" || ug.Probabilities[Today] != 0.85 {
		t.Errorf("urgency.Answer() = %+v, want it keyed by Urgency", ug)
	}
}

func TestAskPlainForms(t *testing.T) {
	t.Parallel()

	// The plain Choice and Score read back as the plain answers.
	var req SystemOneRequest
	tone := Ask(&req, "tone", Choice{Criteria: Labels("calm")})
	rating := Ask(&req, "rating", Score{Criteria: Levels("low")})

	hasType[Key[ChoiceAnswer]](tone)
	hasType[Key[ScoreAnswer]](rating)

	if tone.Name() != "tone" || rating.Name() != "rating" {
		t.Errorf("Name() = %q, %q", tone.Name(), rating.Name())
	}
}

func TestAskRegistersAndReplaces(t *testing.T) {
	t.Parallel()

	var req SystemOneRequest // Questions starts nil.
	Ask(&req, "q", Noul{Instructions: "first"})
	Ask(&req, "q", Noul{Instructions: "second"})

	if len(req.Questions) != 1 {
		t.Fatalf("Questions has %d entries, want 1", len(req.Questions))
	}
	if got := req.Questions["q"].(Noul).Instructions; got != "second" {
		t.Errorf("Questions[q] = %v, want the later registration", got)
	}
}

func TestKeyAnswerDisagreements(t *testing.T) {
	t.Parallel()

	team := Ask(&SystemOneRequest{}, "team", ChoiceOf[Team]{Criteria: LabelsOf(Billing, Technical)})
	urgency := Ask(&SystemOneRequest{}, "urgency", ScoreOf[Urgency]{Criteria: Levels("low", "high")})
	urgent := Ask(&SystemOneRequest{}, "urgent", Noul{Instructions: "?"})

	respond := func(answers map[string]Answer) *SystemOneResponse {
		return &SystemOneResponse{ResponseMeta: ResponseMeta{RequestID: "req-typed"}, Answers: answers}
	}

	tests := []struct {
		name string
		read func() error
		want string
	}{
		{
			"choice the question never offered",
			func() error {
				_, err := team.Answer(respond(map[string]Answer{"team": ChoiceAnswer{Choice: "sales", Probabilities: map[string]float64{"sales": 1}}}))
				return err
			},
			"answers.team.choice",
		},
		{
			"probability for a label never offered",
			func() error {
				_, err := team.Answer(respond(map[string]Answer{"team": ChoiceAnswer{Choice: "billing", Probabilities: map[string]float64{"billing": 0.6, "sales": 0.4}}}))
				return err
			},
			"answers.team.probabilities.sales",
		},
		{
			"level outside the rubric",
			func() error {
				_, err := urgency.Answer(respond(map[string]Answer{"urgency": ScoreAnswer{Legend: map[int]any{0: "low", 1: "high", 5: "?"}, Probabilities: map[int]float64{0: 1}}}))
				return err
			},
			"answers.urgency.legend.5",
		},
		{
			"negative level",
			func() error {
				_, err := urgency.Answer(respond(map[string]Answer{"urgency": ScoreAnswer{Legend: map[int]any{0: "low"}, Probabilities: map[int]float64{-1: 1}}}))
				return err
			},
			"answers.urgency.probabilities.-1",
		},
		{
			"answer of another kind",
			func() error {
				_, err := urgent.Answer(respond(map[string]Answer{"urgent": ChoiceAnswer{Choice: "a"}}))
				return err
			},
			"answers.urgent.type",
		},
		{
			"unknown answer kind",
			func() error {
				_, err := team.Answer(respond(map[string]Answer{"team": UnknownAnswer{Type: "aurora"}}))
				return err
			},
			"answers.team.type",
		},
		{
			"missing answer",
			func() error {
				_, err := urgent.Answer(respond(map[string]Answer{}))
				return err
			},
			"answers.urgent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.read()

			var responseErr *ResponseError
			if !errors.As(err, &responseErr) {
				t.Fatalf("Answer() error = %v, want *ResponseError", err)
			}
			if responseErr.Field != tt.want {
				t.Errorf("Field = %q, want %q", responseErr.Field, tt.want)
			}
			if responseErr.RequestID != "req-typed" {
				t.Errorf("RequestID = %q, want it carried from the response", responseErr.RequestID)
			}
		})
	}
}

func TestKeyAnswerMisuse(t *testing.T) {
	t.Parallel()

	var zero Key[NoulAnswer]
	if _, err := zero.Answer(&SystemOneResponse{}); err == nil {
		t.Error("a zero Key read an answer, want an error")
	}

	key := Ask(&SystemOneRequest{}, "q", Noul{Instructions: "?"})
	if _, err := key.Answer(nil); err == nil {
		t.Error("Answer(nil) succeeded, want an error")
	}
}

func TestSystemOneAsDecodesTypedLabels(t *testing.T) {
	t.Parallel()

	// The generic answer types carry JSON tags, so a caller's own struct can
	// use them with SystemOneAs and get its label and level types directly.
	type ticket struct {
		Answers struct {
			Team    ChoiceAnswerOf[Team]   `json:"team"`
			Urgency ScoreAnswerOf[Urgency] `json:"urgency"`
		} `json:"answers"`
	}

	client, _ := newTestClient(t, respondJSON(http.StatusOK, typedBody))

	req := SystemOneRequest{State: "Our integration returns 500 on every request."}
	Ask(&req, "team", ChoiceOf[Team]{Criteria: LabelsOf(Billing, Technical)})

	got, err := client.SystemOneAs[ticket](t.Context(), req)
	if err != nil {
		t.Fatalf("SystemOneAs() error = %v", err)
	}
	if got.Answers.Team.Choice != Technical || got.Answers.Urgency.Level() != Today {
		t.Errorf("decoded = %+v, want Technical and Today", got.Answers)
	}
}
