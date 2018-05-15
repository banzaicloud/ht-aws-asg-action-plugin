package plugin

import (
	"errors"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	log "github.com/sirupsen/logrus"
)

type AsGroupController struct {
	Session        *session.Session
	AsgSvc         *autoscaling.AutoScaling
	Ec2Svc         *ec2.EC2
}

type InstanceInfo struct {
	Id                 string
	Asg                string
	Az                 string
	Type               string
	Subnet             string
	InstanceProfileArn string
}

// Detaches the instance from its auto scaling group and decreases the desired size
func (a *AsGroupController) DetachInstance(eventData map[string]string) error {
	instanceId := eventData["instance_id"]
	i, err := a.fetchInstanceInfo(instanceId)
	if err != nil {
		log.WithField("instanceId", instanceId).WithError(err).Errorf("failed to fetch instance info")
		return err
	}

	asg, err := a.fetchAsgInfo(&i.Asg)
	if err != nil {
		return err
	}
	log.WithFields(log.Fields{
		"autoScalingGroup": &asg.AutoScalingGroupName,
		"instanceId":       instanceId,
	}).Infof("Fetched auto scaling group info: %#v", *asg)

	err = a.detachInstance(i, asg)
	if err != nil {
		return err
	}
	return nil
}

func (a *AsGroupController) DetachInstanceAndTerminate(eventData map[string]string) error {
	err := a.DetachInstance(eventData)
	if err != nil {
		return err
	}
	instanceId := eventData["instance_id"]
	_, err = a.Ec2Svc.TerminateInstances(&ec2.TerminateInstancesInput{
		InstanceIds: aws.StringSlice([]string{instanceId}),
	})
	if err != nil {
		log.WithFields(log.Fields{
			"autoScalingGroup": eventData["asg_name"],
			"instanceId":       instanceId,
		}).WithError(err).Errorf("failed to terminate instance '%s'", instanceId)
	}
	return nil
}

func (a *AsGroupController) fetchInstanceInfo(instanceId string) (*InstanceInfo, error) {
	asInstances, err := a.AsgSvc.DescribeAutoScalingInstances(&autoscaling.DescribeAutoScalingInstancesInput{
		InstanceIds: aws.StringSlice([]string{instanceId}),
	})
	if err != nil {
		return nil, err
	} else if len(asInstances.AutoScalingInstances) < 1 {
		return nil, errors.New("couldn't find auto scaling group that contains instance")
	}
	asgName := *asInstances.AutoScalingInstances[0].AutoScalingGroupName
	az := *asInstances.AutoScalingInstances[0].AvailabilityZone
	log.WithFields(log.Fields{
		"autoScalingGroup": asgName,
		"instanceId":       instanceId,
	}).Info("Found auto scaling group that contains instance")
	instances, err := a.Ec2Svc.DescribeInstances(&ec2.DescribeInstancesInput{
		InstanceIds: aws.StringSlice([]string{instanceId}),
	})
	if err != nil {
		return nil, err
	} else if len(instances.Reservations) < 1 {
		return nil, errors.New("there is no reservation info in the describe instance response")
	}
	info := &InstanceInfo{
		Id:     instanceId,
		Asg:    asgName,
		Az:     az,
		Type:   *instances.Reservations[0].Instances[0].InstanceType,
		Subnet: *instances.Reservations[0].Instances[0].SubnetId,
	}
	if instances.Reservations[0].Instances[0].IamInstanceProfile != nil {
		info.InstanceProfileArn = *instances.Reservations[0].Instances[0].IamInstanceProfile.Arn
	}
	return info, nil
}

func (a *AsGroupController) detachInstance(i *InstanceInfo, asg *autoscaling.Group) error {
	// change ASG min size if needed so we can detach the instance
	if *asg.MinSize >= *asg.DesiredCapacity {
		_, err := a.AsgSvc.UpdateAutoScalingGroup(&autoscaling.UpdateAutoScalingGroupInput{
			AutoScalingGroupName: asg.AutoScalingGroupName,
			MinSize:              aws.Int64(*asg.MinSize - 1),
		})
		if err != nil {
			log.WithFields(log.Fields{
				"autoScalingGroup": *asg.AutoScalingGroupName,
				"instanceId":       i.Id,
			}).WithError(err).Error("failed to change min size of auto scaling group")
			return err
		}
		log.WithFields(log.Fields{
			"autoScalingGroup": i.Asg,
			"instanceId":       i.Id,
		}).Infof("changed auto scaling group's min size to %d", *asg.MinSize-1)
	}
	_, err := a.AsgSvc.DetachInstances(&autoscaling.DetachInstancesInput{
		AutoScalingGroupName:           asg.AutoScalingGroupName,
		ShouldDecrementDesiredCapacity: aws.Bool(true),
		InstanceIds:                    aws.StringSlice([]string{i.Id}),
	})
	if err != nil {
		log.WithFields(log.Fields{
			"autoScalingGroup": *asg.AutoScalingGroupName,
			"instanceId":       i.Id,
		}).WithError(err).Error("failed to detach instance")
		return err
	}
	return nil
}

func (a *AsGroupController) fetchAsgInfo(name *string) (*autoscaling.Group, error) {
	asgs, err := a.AsgSvc.DescribeAutoScalingGroups(&autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{name},
	})
	if err != nil {
		return nil, err
	}
	return asgs.AutoScalingGroups[0], nil

}
