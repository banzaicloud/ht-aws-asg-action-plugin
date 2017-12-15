package plugin

import (
	"time"

	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/sirupsen/logrus"
)

func upscaleASG(session *session.Session, name string) error {
	// if the launch config is properly set, we don't have too many things to do here
	log.WithFields(logrus.Fields{
		"autoScalingGroup": name,
	}).Info("ASG is upscaling: ", name)
	for i := 0; i < 10; i++ {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": name,
		}).Info(i, "... updating ASG ", name)
		time.Sleep(1 * time.Second)
	}
	return nil
}
