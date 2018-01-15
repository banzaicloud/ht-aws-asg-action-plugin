package main

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	as "github.com/banzaicloud/hollowtrees/actionserver"
	"github.com/banzaicloud/ht-aws-asg-action-plugin/conf"
	"github.com/banzaicloud/ht-aws-asg-action-plugin/plugin"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

var log *logrus.Entry

func init() {
	log = conf.Logger().WithField("package", "main")
}

type AlertHandler struct {
	Router *plugin.EventRouter
}

func newAlertHandler() *AlertHandler {
	region := viper.GetString("plugin.region")
	session, err := session.NewSession(&aws.Config{
		Region: aws.String(region),
	})
	if err != nil {
		log.Fatalf("couldn't create AWS session, cannot start plugin.\n", err)
	}
	return &AlertHandler{
		Router: &plugin.EventRouter{
			Session: session,
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
	port := viper.GetInt("plugin.port")
	fmt.Printf("Starting Hollowtrees ActionServer on port %d", port)
	as.Serve(port, newAlertHandler())
}
