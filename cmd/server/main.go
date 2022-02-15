package main

import (
	"github.com/isrc-cas/gt/server"
	"github.com/rs/zerolog/log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	s, err := server.New(os.Args)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create server")
	}
	defer s.Close()
	err = s.Start()
	if err != nil {
		s.Logger.Fatal().Err(err).Msg("failed to start")
	}

	osSig := make(chan os.Signal, 1)
	signal.Notify(osSig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)

	select {
	case sig := <-osSig:
		s.Logger.Info().Str("signal", sig.String()).Msg("received os signal")
	}
}
