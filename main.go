package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apex/log"
	"github.com/apex/log/handlers/json"
	"github.com/apex/log/handlers/text"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/external"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gorilla/mux"
)

type YoutubeBackup struct {
	FilePath string
}

var views = template.Must(template.ParseGlob("templates/*.html"))

func main() {

	if os.Getenv("UP_STAGE") == "" {
		log.SetHandler(text.Default)
	} else {
		log.SetHandler(json.Default)
	}

	addr := ":" + os.Getenv("PORT")
	app := mux.NewRouter()
	app.HandleFunc("/", today)
	app.HandleFunc("/v", showVideos)
	if err := http.ListenAndServe(addr, app); err != nil {
		log.WithError(err).Fatal("error listening")
	}
}

func today(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, fmt.Sprintf("/v?date=%s", time.Now().AddDate(0, 0, -1).Format("2006-01-02")), http.StatusFound)
}

func allowed(r *http.Request) error {
	if r.Header.Get("X-Forwarded-For") == "" {
		return errors.New("missing X-Forwarded-For")
	}

	remoteAddr := strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]

	whitelist := []string{
		"81.187.180.129/26",
		"210.23.22.2/32",
	}

	for _, network := range whitelist {
		_, subnet, _ := net.ParseCIDR(network)
		ip := net.ParseIP(remoteAddr)
		if subnet.Contains(ip) {
			return nil
		}
	}
	return fmt.Errorf("%s not in whitelist", remoteAddr)
}

func showVideos(w http.ResponseWriter, r *http.Request) {
	if err := allowed(r); err != nil {
		log.WithError(err).Info("not allowed")
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	log.WithField("remoteaddr", r.Header.Get("X-Forwarded-For")).Info("from")
	date := r.FormValue("date")
	ctx := log.WithFields(log.Fields{
		"date": date,
	})

	tz := "Asia/Singapore"
	_, err := parseDate(date, tz)
	if err != nil {
		ctx.WithError(err).Error("bad date")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg, err := external.LoadDefaultAWSConfig(external.WithSharedConfigProfile("mine"))
	if err != nil {
		log.WithError(err).Fatal("failed to load config")
	}
	cfg.Region = "eu-west-1"
	svc := s3.New(cfg)
	req := svc.ListObjectsRequest(&s3.ListObjectsInput{
		Bucket: aws.String("c.prazefarm.co.uk"),
		Prefix: aws.String(date),
	})
	p := s3.NewListObjectsPaginator(req)
	var listing []YoutubeBackup
	for p.Next(context.TODO()) {
		page := p.CurrentPage()
		for _, obj := range page.Contents {
			ext := filepath.Ext(*obj.Key)
			if ext == ".mp4" {
				listing = append(listing, YoutubeBackup{FilePath: strings.TrimSuffix(*obj.Key, ext)})
			}
		}
	}

	if p.Err() != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = views.ExecuteTemplate(w, "index.html", struct {
		Listing []YoutubeBackup
	}{
		listing,
	})

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

}

func parseDate(input string, tz string) (day time.Time, err error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		log.WithError(err).Error("bad timezone")
		return
	}

	day, err = time.ParseInLocation("2006-01-02", input, loc)
	if err != nil {
		log.WithError(err).Info("bad date")
		return
	}

	return
}
