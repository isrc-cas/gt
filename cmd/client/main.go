package main

import (
	"github.com/isrc-cas/gt/client"
	"github.com/rs/zerolog/log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	c, err := client.New(os.Args)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create client")
	}
	defer c.Close()
	err = c.Start()
	if err != nil {
		c.Logger.Fatal().Err(err).Msg("failed to start")
	}

	osSig := make(chan os.Signal, 1)
	signal.Notify(osSig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)

	select {
	case sig := <-osSig:
		c.Logger.Info().Str("signal", sig.String()).Msg("received os signal")
	}
}
