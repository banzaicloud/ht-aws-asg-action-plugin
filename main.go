package main

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	as "github.com/banzaicloud/hollowtrees/actionserver"
	"github.com/banzaicloud/ht-aws-asg-action-plugin/conf"
	"github.com/sirupsen/logrus"
	"github.com/banzaicloud/ht-aws-asg-action-plugin/plugin"
	"github.com/spf13/viper"
)

var log *logrus.Entry

func init() {
	log = conf.Logger().WithField("package", "main")
}

// ASGAlertHandler : dummy implementation of AlertHandler
type ASGAlertHandler struct {
	session *session.Session
}

func newASGAlertHandler() *ASGAlertHandler {
	region := viper.GetString("dev.aws.region")
	session, err := session.NewSession(&aws.Config{
		Region: aws.String(region),
	})
	if err != nil {
		log.Error("Error creating session: ", err)
		panic("couldn't create AWS session, cannot start plugin.")
	}
	return &ASGAlertHandler{
		session: session,
	}
}

// Handle : dummy implementation that returns the alert event's name
func (d *ASGAlertHandler) Handle(event *as.AlertEvent) (*as.ActionResult, error) {
	fmt.Printf("got GRPC request, handling alert: %v\n", event)
	err := plugin.RouteEvent(d.session, event.Resource.ResourceId, event.EventType)
	if err != nil {
		return nil, err
	}
	return &as.ActionResult{Status: "ok"}, nil
}

func main() {
	fmt.Println("Starting Hollowtrees ActionServer")
	as.Serve(newASGAlertHandler())
}
