// Command demo asks a few questions about a support ticket.
//
// Set TYPESAFE_API_KEY, then: go run ./examples/demo
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	typesafe "serge.ax/go/typesafe-sdk-go"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	client, err := typesafe.New()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	models, err := client.Models.List(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "available models:")
	for _, model := range models {
		fmt.Fprintf(os.Stderr, "  %s (%s) — %s\n", model.Name, model.ReleaseDate, model.Description)
	}

	ticket := map[string]any{
		"subject": "Duplicate charge",
		"message": "I was charged twice. Please fix this ASAP.",
	}

	result, err := client.SystemOne(ctx, ticket, typesafe.Questions{
		"billing": typesafe.Noul{
			Instructions: "Is this ticket about billing?",
			Criteria: &typesafe.NoulCriteria{
				True:  "Payments, invoices, refunds, or charges",
				False: "Anything else",
			},
		},
		"tone": typesafe.Choice{
			Instructions: "What is the tone of this message?",
			Criteria: map[string]typesafe.Content{
				"angry":   "An upset or hostile message",
				"calm":    "A neutral or polite message",
				"excited": "An enthusiastic or eager message",
			},
		},
		"urgency": typesafe.Score{
			Instructions: "How urgent is this message?",
			Criteria: []typesafe.Content{
				"Can wait",
				"Needs attention this week",
				"Needs attention today",
			},
		},
	})
	if err != nil {
		if apiErr, ok := errors.AsType[*typesafe.APIError](err); ok && apiErr.RequestID != "" {
			return fmt.Errorf("%w — quote request %s to support", err, apiErr.RequestID)
		}
		return err
	}

	fmt.Printf("model:   %s (%d input tokens)\n", result.Model, result.Usage.InputTokens)
	fmt.Printf("billing: %.0f%% likely\n", result.Nouls()["billing"].Noul*100)

	tone := result.Choices()["tone"]
	fmt.Printf("tone:    %s (%.0f%% confident)\n", tone.Choice, tone.Confidence*100)

	urgency := result.Scores()["urgency"]
	fmt.Printf("urgency: %.1f — %v\n", urgency.Score, urgency.Legend[int(urgency.Score+0.5)])
	return nil
}
