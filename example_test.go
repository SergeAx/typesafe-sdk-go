package typesafe_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	typesafe "serge.ax/go/typesafe-sdk-go"
)

func Example() {
	client, err := typesafe.New()
	if err != nil {
		log.Fatal(err)
	}

	result, err := client.SystemOne(context.Background(),
		"I was charged twice. Please fix this ASAP.",
		typesafe.Questions{
			"category": typesafe.Choice{
				Instructions: "What is this ticket about?",
				Criteria: map[string]typesafe.Content{
					"billing": nil, "technical": nil, "other": nil,
				},
			},
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.Choices()["category"].Choice)
}

func ExampleClient_SystemOne_mixedQuestions() {
	client, err := typesafe.New()
	if err != nil {
		log.Fatal(err)
	}

	result, err := client.SystemOne(context.Background(),
		map[string]any{"subject": "Duplicate charge", "message": "I was charged twice. Please help."},
		typesafe.Questions{
			"billing": typesafe.Noul{Instructions: "Is this about billing?"},
			"tone": typesafe.Choice{
				Instructions: "What is the tone of this message?",
				Criteria:     map[string]typesafe.Content{"angry": "Upset or hostile", "calm": "Neutral or polite"},
			},
			"urgency": typesafe.Score{
				Instructions: "How urgent is this message?",
				Criteria:     []typesafe.Content{"Can wait", "Needs attention this week", "Needs attention today"},
			},
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("billing: %.2f\n", result.Nouls()["billing"].Noul)
	fmt.Printf("tone: %s\n", result.Choices()["tone"].Choice)

	urgency := result.Scores()["urgency"]
	fmt.Printf("urgency: %.1f (%v)\n", urgency.Score, urgency.Legend[2])
}

func ExampleAPIError() {
	client, err := typesafe.New()
	if err != nil {
		log.Fatal(err)
	}

	_, err = client.SystemOne(context.Background(), "state", typesafe.Questions{"a": typesafe.Noul{}})

	switch {
	case err == nil:
	case errors.Is(err, typesafe.ErrRateLimit):
		apiErr, _ := errors.AsType[*typesafe.APIError](err)
		if delay, ok := apiErr.RetryAfter(); ok {
			fmt.Printf("rate limited, retry in %s\n", delay)
		}
	case errors.Is(err, typesafe.ErrTimeout):
		fmt.Println("the request timed out")
	default:
		fmt.Println(err)
	}
}

func ExampleNew_options() {
	retry := typesafe.DefaultRetryPolicy()
	retry.MaxRetries = 5
	retry.RetryStatus = func(status int) bool { return status == 429 || status >= 500 }

	client, err := typesafe.New(
		typesafe.WithAPIKey("sk-..."),
		typesafe.WithDefaultModel("jev-latest"),
		typesafe.WithTimeout(30*time.Second),
		typesafe.WithRetry(retry),
		typesafe.WithHeader("X-Tenant", "acme"),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(client.DefaultModel())
	// Output: jev-latest
}

func ExampleModels_List() {
	client, err := typesafe.New()
	if err != nil {
		log.Fatal(err)
	}

	models, err := client.Models.List(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	for _, model := range models {
		fmt.Printf("%s (%s): %s\n", model.Name, model.ReleaseDate, model.Description)
	}
}
