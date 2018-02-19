package plugin

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	as "github.com/banzaicloud/hollowtrees/actionserver"
	log "github.com/sirupsen/logrus"
)

type EventRouter struct {
	Session *session.Session
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
		if err := a.SwapInstance(event.Data); err != nil {
			return err
		}
	case "prometheus.server.alert.SpotInstanceTooExpensive":
		a := AsGroupController{
			Session: d.Session,
			AsgSvc:  autoscaling.New(d.Session, aws.NewConfig()),
			Ec2Svc:  ec2.New(d.Session, aws.NewConfig()),
		}
		if err := a.SwapInstanceAndTerminate(event.Data); err != nil {
			return err
		}
	}
	return nil
}
