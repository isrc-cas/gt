package main

import (
	"github.com/isrc-cas/gt/logger"
	"github.com/isrc-cas/gt/server"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	s, err := server.New(os.Args)
	if err != nil {
		logger.Fatal().Err(err).Send()
	}
	defer logger.Close()
	err = s.Start()
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to start")
	}
	defer s.Close()

	osSig := make(chan os.Signal, 1)
	signal.Notify(osSig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)

	select {
	case sig := <-osSig:
		logger.Info().Str("signal", sig.String()).Msg("received os signal")
	}
}
