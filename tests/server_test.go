package simplestdioplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	simplestdioplugin "github.com/concernedmate/simple-stdio-plugin"
)

func TestLongInput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	mapped, err := simplestdioplugin.StartPlugin(simplestdioplugin.StartPluginConfig{
		Ctx:       ctx,
		BaseDir:   "./clients",
		Extension: "exe",
		LogFunc:   func(s string) { fmt.Println(s) },
	})
	if err != nil {
		t.Log(err.Error())
	}

	for range 1000 {
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

		plugins := mapped.GetPluginNames()
		for len(plugins) == 0 {
			plugins = mapped.GetPluginNames()
		}

		plugin, err := mapped.GetPluginByName("main.exe")
		if err != nil {
			t.Errorf("shouldnt be error, err: %s", err.Error())
		}

		result, err := plugin.Command(simplestdioplugin.MessageInput{Function: "command", Data: input})
		if err != nil {
			t.Errorf("shouldnt be error, err: %s", err.Error())
		}

		if string(result) != string(input) {
			t.Errorf("invalid response, got: %s", string(result))
		}
	}
}
