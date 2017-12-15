package plugin

import (
	"time"

	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/sirupsen/logrus"
)

func downscaleASG(session *session.Session, name string) error {
	// we can check if the most expensive vm will be detached or not
	log.WithFields(logrus.Fields{
		"autoScalingGroup": name,
	}).Info("ASG is downscaling: ", name)
	for i := 0; i < 32; i++ {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": name,
		}).Info(i, "... updating ASG ", name)
		time.Sleep(1 * time.Second)
	}
	return nil
}
