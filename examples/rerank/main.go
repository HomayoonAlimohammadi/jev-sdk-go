// Command rerank orders search results by how well each answers a query, with
// every candidate rated in a single call.
//
// It shows several questions sharing one state (the query), structured
// instructions that carry each candidate, and the difference between the
// expected Score, which ranks finely, and Level, which buckets coarsely.
//
//	TYPESAFE_API_KEY=... go run ./examples/rerank
package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"strconv"
	"time"

	jev "github.com/HomayoonAlimohammadi/jev-sdk-go"
)

// Relevance is how well a document answers the query.
type Relevance int

const (
	Unrelated Relevance = iota
	Related
	Partial
	Direct
)

type document struct {
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

const query = "How do I rotate an expired TLS certificate on the ingress controller?"

var candidates = []document{
	{"Ingress TLS termination", "Configure a TLS secret on the Ingress and reference it under spec.tls."},
	{"Rotating certificates with cert-manager", "Renew and rotate certificates automatically; force renewal with cmctl renew."},
	{"Cluster upgrade checklist", "Back up etcd, drain nodes and upgrade the control plane first."},
	{"Debugging 502s from the ingress", "Check backend readiness probes and service endpoints."},
}

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, out io.Writer, opts ...jev.ClientOption) error {
	client, err := jev.New(opts...)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	rubric := jev.Levels(
		"unrelated to the query",
		"on the topic, but does not help answer it",
		"answers part of the query",
		"answers the query directly",
	)

	// One question per candidate, all about the same query: a single call
	// rates them all. Instructions can be structured, so each carries its
	// document.
	req := jev.SystemOneRequest{State: query}
	keys := make([]jev.Key[jev.ScoreAnswerOf[Relevance]], len(candidates))
	for i, doc := range candidates {
		keys[i] = jev.Ask(&req, "doc_"+strconv.Itoa(i), jev.ScoreOf[Relevance]{
			Instructions: map[string]any{"task": "Rate how well this document answers the query.", "document": doc},
			Criteria:     rubric,
		})
	}

	resp, err := client.SystemOne(ctx, req)
	if err != nil {
		return err
	}

	type ranked struct {
		doc   document
		score jev.ScoreAnswerOf[Relevance]
	}
	results := make([]ranked, len(candidates))
	for i, key := range keys {
		score, err := key.Answer(resp)
		if err != nil {
			return err
		}
		results[i] = ranked{candidates[i], score}
	}

	// Rank on the expected score: 2.4 and 2.6 both round to level 2, yet the
	// second is the better bet.
	slices.SortStableFunc(results, func(a, b ranked) int {
		return cmp.Compare(b.score.Score, a.score.Score)
	})

	fmt.Fprintf(out, "query: %s\n", query)
	for i, r := range results {
		fmt.Fprintf(out, "%d. %.2f  %-40s %s\n", i+1, r.score.Score, r.doc.Title, r.score.Description())
	}
	return nil
}
