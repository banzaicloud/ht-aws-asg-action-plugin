package plugin

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	as "github.com/banzaicloud/hollowtrees/actionserver"
	"github.com/banzaicloud/ht-aws-asg-action-plugin/conf"
	"github.com/sirupsen/logrus"
)

type EventRouter struct {
	Session *session.Session
}

var log *logrus.Entry

func init() {
	log = conf.Logger().WithField("package", "plugin")
}

func (d *EventRouter) RouteEvent(event *as.AlertEvent) error {
	log.Infof("Received %s", event.EventType)
	switch event.EventType {
	case "prometheus.server.alert.SpotTerminationNotice":
		a := AsGroupController{
			Session: d.Session,
			AsgSvc:  autoscaling.New(d.Session, aws.NewConfig()),
			Ec2Svc:  ec2.New(d.Session, aws.NewConfig()),
		}
		if err := a.SwapInstance(event.Data["instance_id"]); err != nil {
			return err
		}
	}
	return nil
}
