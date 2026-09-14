package ds4_test

import (
	"fmt"
	"log"

	ds4 "github.com/NimbleMarkets/ds4go"
)

func ExampleListModels() {
	catalog, err := ds4.ListModels()
	if err != nil {
		log.Print(err)
		return
	}
	byAlias := make(map[string]ds4.ModelInfo, len(catalog))
	for _, model := range catalog {
		byAlias[model.Alias] = model
	}
	for _, model := range catalog {
		if !model.Installed || !model.IsChatModel() {
			continue
		}
		// Use Alias and Notes as labels; keep Path as the selection's value.
		visionReady := model.Vision && byAlias[model.Encoder].Installed
		fmt.Printf("%s: %s (default=%t, vision=%t)\n",
			model.Alias, model.Notes, model.Default, visionReady)
		// After selection: opts.ModelPath = model.Path; ds4.ApplyVisionDefaults(&opts).
	}
}
