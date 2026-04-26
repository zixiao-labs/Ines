// Command ines runs the Ines language daemon. It speaks the Logos IPC
// protocol over stdio so the editor can spawn the binary as a child process
// and exchange length-prefixed JSON frames with it.
//
// Languages are wired in via blank imports below; each adapter calls
// lang.Register from its package init function.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/zixiao-labs/ines/internal/buildinfo"
	"github.com/zixiao-labs/ines/internal/index"
	"github.com/zixiao-labs/ines/internal/ipc"
	"github.com/zixiao-labs/ines/internal/metrics"

	_ "github.com/zixiao-labs/ines/internal/lang/c"
	_ "github.com/zixiao-labs/ines/internal/lang/golang"
	_ "github.com/zixiao-labs/ines/internal/lang/java"
	_ "github.com/zixiao-labs/ines/internal/lang/rust"
	_ "github.com/zixiao-labs/ines/internal/lang/swift"
	_ "github.com/zixiao-labs/ines/internal/lang/typescript"
)

func main() {
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *versionFlag {
		fmt.Printf("ines %s\n", buildinfo.Version)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		cancel()
	}()

	codec := ipc.NewCodec(os.Stdin, os.Stdout)
	indexer := index.NewIndexer()
	reporter := metrics.NewReporter()
	server := ipc.NewServer(codec, indexer, reporter)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "ines: server exited with error: %v\n", err)
		os.Exit(1)
	}
}
