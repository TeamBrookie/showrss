package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/mux"
	"github.com/teambrookie/showrss/betaseries"
	"github.com/teambrookie/showrss/dao"
	"github.com/teambrookie/showrss/handlers"
	"github.com/teambrookie/showrss/worker"

	"flag"
	"syscall"

	"cloud.google.com/go/firestore"
)

const version = "1.0.0"

func main() {

	var httpAddr = flag.String("http", "0.0.0.0:8000", "HTTP service address")
	flag.Parse()

	apiKey := os.Getenv("BETASERIES_KEY")
	if apiKey == "" {
		log.Fatalln("BETASERIES_KEY must be set in env")
	}

	episodeProvider := betaseries.Betaseries{APIKey: apiKey}

	log.Println("Starting showrss ...")
	log.Printf("HTTP service listening on %s", *httpAddr)

	//Intialize Firestore client
	ctx := context.Background()
	client, err := firestore.NewClient(ctx, "showrss-64e4b")
	if err != nil {
		log.Fatalf("Error when initializing the firestore client : %s\n", err)
	}
	log.Println("Firestore connection OK ...")

	datastore := &dao.Datastore{Store: client}

	// Worker stuff
	log.Println("Starting worker ...")
	torrentSearchs := make(chan dao.Episode, 1000)
	updateEpisode := make(chan dao.Episode, 100)
	limiter := make(chan time.Time, 10)
	go func() {
		for t := range time.Tick(time.Hour * 1) {
			limiter <- t
		}
	}()
	go worker.TorrentSearch(torrentSearchs, updateEpisode, client)
	go worker.UpdateEpisode(datastore, updateEpisode)
	go worker.Refresh(limiter, torrentSearchs, datastore, episodeProvider)
	errChan := make(chan error, 10)

	mux := mux.NewRouter()
	mux.HandleFunc("/", handlers.HelloHandler)
	mux.Handle("/auth", handlers.AuthHandler(datastore, episodeProvider))
	mux.Handle("/refreshes", handlers.RefreshHandler(limiter))
	mux.Handle("/{user}/episodes", handlers.EpisodeHandler(datastore))
	mux.Handle("/{user}/rss", handlers.RSSHandler(datastore))

	httpServer := http.Server{}
	httpServer.Addr = *httpAddr
	httpServer.Handler = handlers.LoggingHandler(mux)

	go func() {
		errChan <- httpServer.ListenAndServe()
	}()

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case err := <-errChan:
			if err != nil {
				log.Fatal(err)
			}
		case s := <-signalChan:
			log.Println(fmt.Sprintf("Captured %v. Exiting...", s))
			httpServer.Shutdown(context.Background())
			os.Exit(0)
		}
	}

}
