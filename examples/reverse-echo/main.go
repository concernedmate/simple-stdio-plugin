package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"

	simplestdioplugin "github.com/concernedmate/simple-stdio-plugin"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	plugins, err := simplestdioplugin.StartPlugin(
		ctx, simplestdioplugin.StartPluginConfig{
			BaseDir:   "./plugin",
			Extension: "exe",
			LogFunc:   func(s string) { fmt.Println(s) },
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	plugin, err := plugins.GetPluginByName("main.exe")
	if err != nil {
		fmt.Println("please compile main.go on plugin dir")
		log.Fatal(err)
	}

	fmt.Println("Enter text to be reversed by plugin...")

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		input := scanner.Text()
		result, err := plugin.Command(simplestdioplugin.MessageInput{
			Function: "reverse",
			Data:     []byte(input),
		})
		if err != nil {
			log.Fatal(err)
		}

		fmt.Println(string(result))
	}
	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
}
