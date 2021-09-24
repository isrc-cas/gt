package main

import (
	"github.com/isrc-cas/gt/client"
	"github.com/isrc-cas/gt/logger"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	c, err := client.New(os.Args)
	if err != nil {
		logger.Fatal().Err(err).Send()
	}
	defer logger.Close()
	err = c.Start()
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to start")
	}
	defer c.Close()

	osSig := make(chan os.Signal, 1)
	signal.Notify(osSig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)

	select {
	case sig := <-osSig:
		logger.Info().Str("signal", sig.String()).Msg("received os signal")
	}
}
