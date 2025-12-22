package simplestdioplugin

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	simplestdioplugin "github.com/concernedmate/simple-stdio-plugin"
)

func TestLongInput(t *testing.T) {
	ctx := t.Context()

	mapped := simplestdioplugin.NewPluginMap(func(s string) { fmt.Println(s) })
	go func() {
		if err := simplestdioplugin.PluginRunnerWithContext(ctx, &mapped, "./clients", "exe"); err != nil {
			t.Log(err.Error())
		}
	}()

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

	command := "command?json=" + string(input)
	result, err := plugin.Command([]byte(command))
	if err != nil {
		t.Errorf("shouldnt be error, err: %s", err.Error())
	}

	if string(result) != string(input) {
		t.Errorf("invalid response, got: %s", string(result))
	}
}
