// Package jev is a Go client for the TypeSafe AI API.
//
// TypeSafe answers structured questions about a piece of content, called the
// state. You name each question; the answers come back under those names, with
// the probabilities behind them.
//
// # Quickstart
//
// Set TYPESAFE_API_KEY in the environment, then:
//
//	client, err := jev.New()
//	if err != nil {
//	    return err
//	}
//
//	resp, err := client.SystemOne(ctx, jev.SystemOneRequest{
//	    State: "I was charged twice. Please fix this ASAP.",
//	    Questions: map[string]jev.Question{
//	        "category": jev.Choice{
//	            Instructions: "What is this ticket about?",
//	            Criteria:     jev.Labels("billing", "technical", "other"),
//	        },
//	    },
//	})
//	if err != nil {
//	    return err
//	}
//
//	fmt.Println(resp.Choices["category"].Choice)
//
// # Questions
//
// There are three kinds. [Noul] asks a yes/no question and answers with a
// probability. [Choice] selects one of several named labels. [Score] rates the
// state against an ordered rubric. [RawQuestion] passes an arbitrary object
// through, for question kinds the API accepts before this package models them.
//
// # Errors
//
// Failures come back as one of three types, each recoverable with
// [errors.As]:
//
//   - [*APIError] for an unsuccessful HTTP response. Use [errors.Is] against
//     [ErrRateLimited], [ErrUnauthorized] and the other status sentinels to
//     branch without inspecting status codes.
//   - [*TransportError] when the request never reached the API.
//   - [*ResponseError] when the response body was missing data the SDK needs,
//     or left one of your questions unanswered. It names the offending field.
//
// # Concurrency
//
// A [Client] is safe for concurrent use and pools connections, so create one
// and share it. Parallelism is the caller's to choose: run calls in
// goroutines, bounded however the program needs.
//
// # Retries
//
// By default a call is retried twice on 408, 429 and 5xx responses and on
// transport failures, with exponential backoff, honoring the server's
// Retry-After, within a 30 second budget. See [RetryPolicy] to change or
// disable that, per client or per call.
//
// # Logging
//
// The SDK logs nothing until [WithLogger] supplies a logger. URLs are stripped
// of credentials and credential-bearing headers are redacted. Request and
// response bodies are left out unless [WithBodyLogging] asks for them: the
// request body is the state being evaluated, so it carries whatever the
// caller's content carries.
//
// Learn what TypeSafe is and what it can do at https://docs.typesafe.ai.
package jev
