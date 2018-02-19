package main

import (
	"flag"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	as "github.com/banzaicloud/hollowtrees/actionserver"
	"github.com/banzaicloud/ht-aws-asg-action-plugin/plugin"
	log "github.com/sirupsen/logrus"
)

var (
	logLevel       = flag.String("log.level", "info", "log level")
	bindAddr       = flag.String("bind.address", ":8080", "Bind address where the gRPC API is listening")
	region         = flag.String("aws.region", "eu-west-1", "The AWS region where the plugin is working")
	recommenderUrl = flag.String("recommender.url", "http://localhost:9090", "URL of the spot instance recommender")
)

func init() {
	flag.Parse()
	parsedLevel, err := log.ParseLevel(*logLevel)
	if err != nil {
		log.WithError(err).Warnf("Couldn't parse log level, using default: %s", log.GetLevel())
	} else {
		log.SetLevel(parsedLevel)
		log.Debugf("Set log level to %s", parsedLevel)
	}
}

type AlertHandler struct {
	Router *plugin.EventRouter
}

func newAlertHandler() *AlertHandler {
	session, err := session.NewSession(&aws.Config{
		Region: aws.String(*region),
	})
	if err != nil {
		log.Fatalf("couldn't create AWS session, cannot start plugin.\n", err)
	}
	return &AlertHandler{
		Router: &plugin.EventRouter{
			Session:        session,
			RecommenderURL: *recommenderUrl,
		},
	}
}

func (d *AlertHandler) Handle(event *as.AlertEvent) (*as.ActionResult, error) {
	fmt.Printf("got GRPC request, handling alert: %v\n", event)
	err := d.Router.RouteEvent(event)
	if err != nil {
		return nil, err
	}
	return &as.ActionResult{Status: "ok"}, nil
}

func main() {
	fmt.Printf("Starting Hollowtrees ActionServer on %s\n", *bindAddr)
	as.Serve(*bindAddr, newAlertHandler())
}
