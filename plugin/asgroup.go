package plugin

import (
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/banzaicloud/ht-aws-asg-action-plugin/util"
	"github.com/banzaicloud/spot-recommender/recommender"
	"github.com/sirupsen/logrus"
)

type AsGroupController struct {
	Session *session.Session
	AsgSvc  *autoscaling.AutoScaling
	Ec2Svc  *ec2.EC2
}

type InstanceInfo struct {
	Id                 string
	Asg                string
	Az                 string
	Type               string
	Subnet             string
	InstanceProfileArn string
}

// Detaches the instance from its auto scaling group and attaches a new one with a recommended instance type
func (a *AsGroupController) SwapInstance(instanceId string) error {

	i, err := a.fetchInstanceInfo(instanceId)
	if err != nil {
		return err
	}

	asg, lc, err := a.fetchAsgInfo(&i.Asg)
	if err != nil {
		return err
	}

	recommendation, err := a.getRecommendation(i)
	if err != nil {
		return err
	}

	err = a.detachInstance(i, asg)
	if err != nil {
		return err
	}

	newInstanceId, err := a.requestAndWaitSpotInstance(recommendation, i, asg, lc)
	if err != nil {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": &asg.AutoScalingGroupName,
			"instanceId":       instanceId,
		}).WithError(err).Error("failed to request a new spot instance instead of the soon-to-be-terminated instance")
		return err
	}

	err = a.attachInstance(i, asg, newInstanceId)
	if err != nil {
		return err
	}

	return nil
}

func (a *AsGroupController) fetchInstanceInfo(instanceId string) (*InstanceInfo, error) {
	asInstances, err := a.AsgSvc.DescribeAutoScalingInstances(&autoscaling.DescribeAutoScalingInstancesInput{
		InstanceIds: aws.StringSlice([]string{instanceId}),
	})
	if err != nil || len(asInstances.AutoScalingInstances) < 1 {
		log.WithField("instanceId", instanceId).WithError(err).Errorf("Couldn't find auto scaling group that contains instance")
		return nil, err
	}
	asgName := *asInstances.AutoScalingInstances[0].AutoScalingGroupName
	az := *asInstances.AutoScalingInstances[0].AvailabilityZone
	log.WithFields(logrus.Fields{
		"autoScalingGroup": asgName,
		"instanceId":       instanceId,
	}).Info("Found auto scaling group that contains instance")
	instances, err := a.Ec2Svc.DescribeInstances(&ec2.DescribeInstancesInput{
		InstanceIds: aws.StringSlice([]string{instanceId}),
	})
	if err != nil || len(instances.Reservations) < 1 {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": asgName,
		}).WithError(err).Errorf("Couldn't describe instance %s", instanceId)
		return nil, err
	}
	info := &InstanceInfo{
		Id:     instanceId,
		Asg:    asgName,
		Az:     az,
		Type:   *instances.Reservations[0].Instances[0].InstanceType,
		Subnet: *instances.Reservations[0].Instances[0].SubnetId,
	}
	if instances.Reservations[0].Instances[0].IamInstanceProfile.Arn != nil {
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
			log.WithFields(logrus.Fields{
				"autoScalingGroup": &asg.AutoScalingGroupName,
				"instanceId":       i.Id,
			}).WithError(err).Error("failed to change min size of auto scaling group")
			return err
		}
	}
	_, err := a.AsgSvc.DetachInstances(&autoscaling.DetachInstancesInput{
		AutoScalingGroupName:           asg.AutoScalingGroupName,
		ShouldDecrementDesiredCapacity: aws.Bool(true),
		InstanceIds:                    aws.StringSlice([]string{i.Id}),
	})
	if err != nil {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": &asg.AutoScalingGroupName,
			"instanceId":       i.Id,
		}).WithError(err).Error("failed to detach instance")
		return err
	}
	return nil
}

func (a *AsGroupController) getRecommendation(i *InstanceInfo) (*recommender.InstanceTypeInfo, error) {
	log.WithFields(logrus.Fields{
		"autoScalingGroup": i.Asg,
		"instanceId":       i.Id,
	}).Info("Getting recommendations in AZ '%s' for base instance type '%s'", i.Az, i.Type)
	recommendations, err := recommender.RecommendSpotInstanceTypes(*a.Session.Config.Region, []string{i.Az}, i.Type)
	if err != nil {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": i.Asg,
			"instanceId":       i.Id,
		}).WithError(err).Error("couldn't get recommendations")
		return nil, err
	}
	log.WithFields(logrus.Fields{
		"autoScalingGroup": i.Asg,
		"instanceId":       i.Id,
	}).Info("Got recommendation: %#v", recommendations)

	recommendation := util.SelectCheapestRecommendation(recommendations[i.Az])
	return &recommendation, nil
}

func (a *AsGroupController) attachInstance(i *InstanceInfo, asg *autoscaling.Group, newInstanceId *string) error {
	_, err := a.AsgSvc.AttachInstances(&autoscaling.AttachInstancesInput{
		InstanceIds:          []*string{newInstanceId},
		AutoScalingGroupName: asg.AutoScalingGroupName,
	})
	if err != nil {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": &asg.AutoScalingGroupName,
			"instanceId":       i.Id,
		}).WithError(err).Error("failed to attach instance to auto scaling group, terminating it")
		_, tErr := a.Ec2Svc.TerminateInstances(&ec2.TerminateInstancesInput{
			InstanceIds: []*string{newInstanceId},
		})
		if tErr != nil {
			log.WithFields(logrus.Fields{
				"autoScalingGroup": &asg.AutoScalingGroupName,
				"instanceId":       i.Id,
			}).WithError(tErr).Errorf("failed to terminate instance '%s'\n", *newInstanceId)
		}
		return err
	}
	// change back ASG min size to original
	_, err = a.AsgSvc.UpdateAutoScalingGroup(&autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: asg.AutoScalingGroupName,
		MinSize:              asg.MinSize,
	})
	if err != nil {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": &asg.AutoScalingGroupName,
			"instanceId":       i.Id,
		}).WithError(err).Error("couldn't change back min size of auto scaling group")
	}
	return nil
}

func (a *AsGroupController) fetchAsgInfo(name *string) (*autoscaling.Group, *autoscaling.LaunchConfiguration, error) {
	asgs, err := a.AsgSvc.DescribeAutoScalingGroups(&autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{name},
	})
	if err != nil {
		return nil, nil, err
	}
	asg := asgs.AutoScalingGroups[0]
	lcs, err := a.AsgSvc.DescribeLaunchConfigurations(&autoscaling.DescribeLaunchConfigurationsInput{
		LaunchConfigurationNames: []*string{asg.LaunchConfigurationName},
	})
	if err != nil {
		return nil, nil, err
	}
	return asg, lcs.LaunchConfigurations[0], nil

}

func (a *AsGroupController) requestAndWaitSpotInstance(recommendation *recommender.InstanceTypeInfo, i *InstanceInfo, asg *autoscaling.Group, lc *autoscaling.LaunchConfiguration) (*string, error) {

	placement := &ec2.SpotPlacement{}
	if asg.PlacementGroup != nil {
		placement.GroupName = asg.PlacementGroup
	}
	if lc.PlacementTenancy != nil {
		placement.Tenancy = lc.PlacementTenancy
	}

	requestSpotInput := &ec2.RequestSpotInstancesInput{
		InstanceCount: aws.Int64(1),
		SpotPrice:     &recommendation.OnDemandPrice,
		LaunchSpecification: &ec2.RequestSpotLaunchSpecification{
			EbsOptimized: lc.EbsOptimized,
			InstanceType: &recommendation.InstanceTypeName,
			ImageId:      lc.ImageId,
			KeyName:      lc.KeyName,
			Monitoring: &ec2.RunInstancesMonitoringEnabled{
				Enabled: lc.InstanceMonitoring.Enabled,
			},
			NetworkInterfaces: []*ec2.InstanceNetworkInterfaceSpecification{
				{
					DeviceIndex:              aws.Int64(0),
					SubnetId:                 &i.Subnet,
					AssociatePublicIpAddress: lc.AssociatePublicIpAddress,
					Groups:                   lc.SecurityGroups,
				},
			},
			Placement: placement,
		},
	}

	if lc.KernelId != nil || *lc.KernelId != "" {
		requestSpotInput.LaunchSpecification.KernelId = lc.KernelId
	}
	if lc.RamdiskId != nil || *lc.RamdiskId != "" {
		requestSpotInput.LaunchSpecification.RamdiskId = lc.RamdiskId
	}
	if lc.UserData != nil || *lc.UserData != "" {
		requestSpotInput.LaunchSpecification.UserData = lc.UserData
	}
	if i.InstanceProfileArn != "" {
		requestSpotInput.LaunchSpecification.IamInstanceProfile = &ec2.IamInstanceProfileSpecification{
			Arn: &i.InstanceProfileArn,
		}
	}

	result, err := a.Ec2Svc.RequestSpotInstances(requestSpotInput)
	if err != nil {
		return nil, err
	}
	requestId := result.SpotInstanceRequests[0].SpotInstanceRequestId

	// wait until the spot request is fulfilled and we have a new instance id
	var instanceId *string
	for instanceId == nil {
		spotRequests, err := a.Ec2Svc.DescribeSpotInstanceRequests(&ec2.DescribeSpotInstanceRequestsInput{
			SpotInstanceRequestIds: []*string{requestId},
		})
		if err != nil {
			return nil, err
		}
		spotRequest := spotRequests.SpotInstanceRequests[0]
		if spotRequest != nil && spotRequest.InstanceId != nil && *spotRequest.InstanceId != "" {
			log.WithFields(logrus.Fields{
				"autoScalingGroup": &asg.AutoScalingGroupName,
				"instanceId":       i.Id,
			}).Info("instanceId in spot request is:", *spotRequest.InstanceId)
			instanceId = spotRequest.InstanceId
		} else {
			log.WithFields(logrus.Fields{
				"autoScalingGroup": &asg.AutoScalingGroupName,
				"instanceId":       i.Id,
			}).Info("instance id in request is null")
		}
		time.Sleep(1 * time.Second)
	}

	// wait until the new instance reaches the running state because only running instances can be attached to an ASG
	var running bool
	for !running {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": &asg.AutoScalingGroupName,
			"instanceId":       i.Id,
		}).Info("Describing instances")
		describeInstResult, err := a.Ec2Svc.DescribeInstanceStatus(&ec2.DescribeInstanceStatusInput{
			InstanceIds: []*string{instanceId},
		})
		if err != nil {
			return nil, err
		}
		if len(describeInstResult.InstanceStatuses) > 0 {
			status := describeInstResult.InstanceStatuses[0]
			log.WithFields(logrus.Fields{
				"autoScalingGroup": &asg.AutoScalingGroupName,
				"instanceId":       i.Id,
			}).Debugf("instance '%s' is %s\n", *instanceId, *status.InstanceState.Name)
			if *status.InstanceState.Name == "running" {
				running = true
				continue
			}
		}
		time.Sleep(1 * time.Second)
	}
	return instanceId, nil
}
