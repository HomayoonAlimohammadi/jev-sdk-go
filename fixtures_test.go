package jev

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files under testdata/ instead of comparing against them")

// responseFixtures lists the response bodies to decode: the schema-derived
// ones always, and any captured from the live API when they have been recorded.
func responseFixtures(t *testing.T) []string {
	t.Helper()

	var paths []string
	for _, pattern := range []string{"testdata/responses/*.json", "testdata/live/*.json"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, matches...)
	}
	if len(paths) == 0 {
		t.Fatal("no response fixtures found under testdata/")
	}
	return paths
}

func TestResponseFixtures(t *testing.T) {
	t.Parallel()

	for _, path := range responseFixtures(t) {
		t.Run(filepath.ToSlash(path), func(t *testing.T) {
			t.Parallel()

			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			meta := ResponseMeta{StatusCode: http.StatusOK, Raw: body}

			if strings.HasPrefix(filepath.Base(path), "models") {
				resp, err := decodeListModels(meta, "")
				if err != nil {
					t.Fatalf("decodeListModels() error = %v", err)
				}
				if len(resp.Models) == 0 || resp.Models[0].Name == "" {
					t.Errorf("Models = %+v, want at least one named model", resp.Models)
				}
				return
			}

			resp, err := decodeSystemOne(meta, nil, "")
			if err != nil {
				t.Fatalf("decodeSystemOne() error = %v", err)
			}
			if resp.Model == "" || len(resp.Answers) == 0 {
				t.Errorf("response = %+v, want a model and answers", resp)
			}
			// Every answer the fixture carries is decoded into some kind, none
			// silently lost.
			var raw struct {
				Answers map[string]json.RawMessage `json:"answers"`
			}
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatal(err)
			}
			if len(resp.Answers) != len(raw.Answers) {
				t.Errorf("decoded %d answers, want all %d", len(resp.Answers), len(raw.Answers))
			}
		})
	}
}

func TestSchemaFixtureValues(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("testdata/responses/systemone.json")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := decodeSystemOne(ResponseMeta{StatusCode: http.StatusOK, Raw: body}, nil, "")
	if err != nil {
		t.Fatalf("decodeSystemOne() error = %v", err)
	}

	tone := resp.Choices["tone"]
	// The schema's own example shows confidence and the chosen label's
	// probability are different measures: 0.9 against 0.8.
	if tone.Choice != "angry" || tone.Confidence != 0.9 || tone.Probabilities["angry"] != 0.8 {
		t.Errorf("tone = %+v, want angry at confidence 0.9 and probability 0.8", tone)
	}
	if urgency := resp.Scores["urgency"]; urgency.Level() != 2 || urgency.Description() != "Needs attention today" {
		t.Errorf("urgency level %d (%v), want 2 (Needs attention today)", urgency.Level(), urgency.Description())
	}
	if *resp.Usage.InputTokens != 120 || *resp.Usage.OutputTokens != 12 {
		t.Errorf("Usage = %+v, want 120 and 12", resp.Usage)
	}
}

func TestStructuredLegendFixture(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("testdata/responses/systemone_structured_legend.json")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := decodeSystemOne(ResponseMeta{StatusCode: http.StatusOK, Raw: body}, nil, "")
	if err != nil {
		t.Fatalf("decodeSystemOne() error = %v", err)
	}

	// The API returns structured levels unchanged, nested nulls included.
	level, ok := resp.Scores["urgency"].Legend[1].(map[string]any)
	if !ok || level["meaning"] != "Needs attention today" {
		t.Fatalf("Legend[1] = %#v, want the structured level as sent", resp.Scores["urgency"].Legend[1])
	}
	examples, _ := level["examples"].([]any)
	if note, _ := examples[1].(map[string]any); len(examples) != 2 || note["note"] != nil {
		t.Errorf("examples = %#v, want the nested null preserved", level["examples"])
	}
}

func TestErrorFixtures(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("testdata/errors/validation_422.json")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := extractMessage(body), "state: Field required"; got != want {
		t.Errorf("extractMessage() = %q, want %q", got, want)
	}
}

// goldenRequest is a request built from the schema's own examples.
func goldenRequest() SystemOneRequest {
	return SystemOneRequest{
		State: "I was charged twice. Please help.",
		Questions: map[string]Question{
			"spam": Noul{
				Instructions: "Is this message spam?",
				Criteria:     &NoulCriteria{True: "Unsolicited advertising", False: "A legitimate conversation"},
			},
			"tone": Choice{
				Instructions: "What is the tone of this message?",
				Criteria: map[string]any{
					"angry":   "An upset or hostile message",
					"calm":    "A neutral or polite message",
					"excited": "An enthusiastic or eager message",
				},
			},
			"urgency": Score{
				Instructions: "How urgent is this message?",
				Criteria:     Levels("Can wait", "Needs attention this week", "Needs attention today"),
			},
		},
	}
}

func TestRequestGolden(t *testing.T) {
	t.Parallel()

	client, rec := newTestClient(t, respondJSON(http.StatusOK, `{"model":"jev-latest","usage":{},"answers":{`+
		`"spam":{"type":"noul","noul":0.5},`+
		`"tone":{"type":"choice","choice":"calm","confidence":1,"probabilities":{"calm":1}},`+
		`"urgency":{"type":"score","score":0,"confidence":1,"legend":{"0":"Can wait"},"probabilities":{"0":1}}}}`))

	if _, err := client.SystemOne(t.Context(), goldenRequest()); err != nil {
		t.Fatalf("SystemOne() error = %v", err)
	}
	_, sent := rec.last()

	var indented bytes.Buffer
	if err := json.Indent(&indented, []byte(sent), "", "  "); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	indented.WriteByte('\n')

	const golden = "testdata/requests/systemone.golden.json"
	if *update {
		if err := os.WriteFile(golden, indented.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(indented.Bytes(), want) {
		t.Errorf("request encoding changed; run with -update if intended.\ngot:\n%s\nwant:\n%s", indented.Bytes(), want)
	}
}
