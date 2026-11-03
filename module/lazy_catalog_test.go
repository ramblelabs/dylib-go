package module_test

import (
	"context"
	"testing"

	dylib "github.com/ramblelabs/dylib-go"
	example "github.com/ramblelabs/dylib-go/examples/modules"
)

func TestLazyCatalogDefersMissingInputs(t *testing.T) {
	dir := t.TempDir()
	mods := example.Manifest("not-installed-provider.o", "not-installed-consumer.o").Modules
	writeCatalog(t, dir, "modules", mods...)
	owner, err := example.LazySymbols(context.Background(), dylib.Options{}, dir)
	if err != nil {
		t.Fatal("creation opened native inputs", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal("unused close opened native inputs", err)
	}
}
