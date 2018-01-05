package plugin

import (
	as "github.com/banzaicloud/hollowtrees/actionserver"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/banzaicloud/ht-aws-asg-action-plugin/conf"
	"github.com/sirupsen/logrus"
)

var log *logrus.Entry

func init() {
	log = conf.Logger().WithField("package", "plugin")
}

func RouteEvent(session *session.Session, event *as.AlertEvent) error {
	name := event.Data["asg_name"]
	switch event.EventType {
	case "initializing":
		if err := initializeASG(session, name); err != nil {
			return err
		}
	case "upscaling":
		if err := upscaleASG(session, name); err != nil {
			return err
		}
	case "downscaling":
		if err := downscaleASG(session, name); err != nil {
			return err
		}
	case "rebalancing":
		if err := rebalanceASG(session, name); err != nil {
			return err
		}
	}
	if err := updateLaunchConfig(session, name); err != nil {
		return err
	}
	return nil
}
