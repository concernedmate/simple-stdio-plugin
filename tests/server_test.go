package simplestdioplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"

	simplestdioplugin "github.com/concernedmate/simple-stdio-plugin"
	"github.com/google/uuid"
)

func TestCallPlugin(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	mapped, err := simplestdioplugin.StartPlugin(
		ctx, simplestdioplugin.StartPluginConfig{
			BaseDir:   "./clients",
			Extension: "exe",
		},
	)
	if err != nil {
		t.Log(err.Error())
	}

	lorem, err := os.ReadFile("./test_cases/lorem.txt")
	if err != nil {
		t.Errorf("shouldnt be error, err: %s", err.Error())
		t.FailNow()
	}

	data := map[string]string{"input": string(lorem)}
	input, err := json.Marshal(data)
	if err != nil {
		t.Errorf("shouldnt be error, err: %s", err.Error())
		t.FailNow()
	}

	for range 50 {
		wg := sync.WaitGroup{}
		for _, val := range mapped.GetPluginNames() {
			plugin, err := mapped.GetPluginByName(val)
			if err != nil {
				t.Errorf("shouldnt be error, err: %s", err.Error())
			}

			for range 5 {
				wg.Go(func() {
					result, err := plugin.Command(simplestdioplugin.MessageInput{Function: "command", Data: input})
					if err != nil {
						t.Errorf("shouldnt be error, err: %s", err.Error())
					}
					if string(result) != string(input) {
						t.Errorf("invalid response, got: %s", string(result))
					}
				})
				wg.Go(func() {
					_, err := plugin.Command(simplestdioplugin.MessageInput{Function: "error", Data: nil})
					if err == nil {
						t.Errorf("should be errored")
					}
				})
			}
		}
		wg.Wait()
	}

	input = []byte(uuid.New().String())
	for range 50 {
		wg := sync.WaitGroup{}
		for _, val := range mapped.GetPluginNames() {
			plugin, err := mapped.GetPluginByName(val)
			if err != nil {
				t.Errorf("shouldnt be error, err: %s", err.Error())
			}

			for range 5 {
				wg.Go(func() {
					result, err := plugin.Command(simplestdioplugin.MessageInput{Function: "command", Data: input})
					if err != nil {
						t.Errorf("shouldnt be error, err: %s", err.Error())
					}
					if string(result) != string(input) {
						t.Errorf("invalid response, got: %s", string(result))
					}
				})
				wg.Go(func() {
					_, err := plugin.Command(simplestdioplugin.MessageInput{Function: "error", Data: nil})
					if err == nil {
						t.Errorf("should be errored")
					}
				})
			}
		}
		wg.Wait()
	}
}

func TestCallServer(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	router := map[string]func(jsons []byte) ([]byte, error){
		"reverse": func(jsons []byte) ([]byte, error) {
			slices.Reverse(jsons)
			return jsons, nil
		},
	}

	mapped, err := simplestdioplugin.StartPlugin(
		ctx, simplestdioplugin.StartPluginConfig{
			BaseDir:   "./clients",
			Extension: "exe",
			Router:    router,
			LogFunc:   func(s string) { fmt.Println(s) },
		},
	)
	if err != nil {
		t.Log(err.Error())
	}

	input := []byte(uuid.New().String())
	for range 50 {
		wg := sync.WaitGroup{}
		for _, val := range mapped.GetPluginNames() {
			plugin, err := mapped.GetPluginByName(val)
			if err != nil {
				t.Errorf("shouldnt be error, err: %s", err.Error())
			}

			for range 5 {
				wg.Go(func() {
					result, err := plugin.Command(simplestdioplugin.MessageInput{Function: "call", Data: input})
					if err != nil {
						t.Errorf("shouldnt be error, err: %s", err.Error())
					}
					slices.Reverse(result)
					if string(result) != string(input) {
						t.Errorf("expected response %s, got: %s", string(input), string(result))
					}
				})
			}
		}
		wg.Wait()
	}
}
