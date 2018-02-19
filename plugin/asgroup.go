package plugin

import (
	"errors"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/banzaicloud/ht-aws-asg-action-plugin/util"
	"github.com/banzaicloud/spot-recommender/recommender"
	log "github.com/sirupsen/logrus"
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
func (a *AsGroupController) SwapInstance(eventData map[string]string) error {
	instanceId := eventData["instance_id"]
	i, err := a.fetchInstanceInfo(instanceId)
	if err != nil {
		log.WithField("instanceId", instanceId).WithError(err).Errorf("failed to fetch instance info")
		return err
	}

	asg, lc, err := a.fetchAsgInfo(&i.Asg)
	if err != nil {
		return err
	}
	log.WithFields(log.Fields{
		"autoScalingGroup": &asg.AutoScalingGroupName,
		"instanceId":       instanceId,
	}).Infof("Fetched auto scaling group info: %#v", *asg)
	log.WithFields(log.Fields{
		"autoScalingGroup": &asg.AutoScalingGroupName,
		"instanceId":       instanceId,
	}).Infof("Fetched launch config info: %#v", *lc)

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
		log.WithFields(log.Fields{
			"autoScalingGroup": *asg.AutoScalingGroupName,
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

func (a *AsGroupController) SwapInstanceAndTerminate(eventData map[string]string) error {
	err := a.SwapInstance(eventData)
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

func (a *AsGroupController) getRecommendation(i *InstanceInfo) (*recommender.InstanceTypeInfo, error) {
	log.WithFields(log.Fields{
		"autoScalingGroup": i.Asg,
		"instanceId":       i.Id,
	}).Infof("Getting recommendations in AZ '%s' for base instance type '%s'", i.Az, i.Type)
	recommendations, err := recommender.RecommendSpotInstanceTypes(*a.Session.Config.Region, []string{i.Az}, i.Type)
	if err != nil {
		log.WithFields(log.Fields{
			"autoScalingGroup": i.Asg,
			"instanceId":       i.Id,
		}).WithError(err).Error("couldn't get recommendations")
		return nil, err
	}
	log.WithFields(log.Fields{
		"autoScalingGroup": i.Asg,
		"instanceId":       i.Id,
	}).Infof("Got recommendation: %#v", recommendations)

	recommendation := util.SelectCheapestRecommendation(recommendations[i.Az])
	return &recommendation, nil
}

func (a *AsGroupController) attachInstance(i *InstanceInfo, asg *autoscaling.Group, newInstanceId *string) error {
	_, err := a.AsgSvc.AttachInstances(&autoscaling.AttachInstancesInput{
		InstanceIds:          []*string{newInstanceId},
		AutoScalingGroupName: asg.AutoScalingGroupName,
	})
	if err != nil {
		log.WithFields(log.Fields{
			"autoScalingGroup": *asg.AutoScalingGroupName,
			"instanceId":       i.Id,
		}).WithError(err).Error("failed to attach instance to auto scaling group, terminating it")
		_, tErr := a.Ec2Svc.TerminateInstances(&ec2.TerminateInstancesInput{
			InstanceIds: []*string{newInstanceId},
		})
		if tErr != nil {
			log.WithFields(log.Fields{
				"autoScalingGroup": *asg.AutoScalingGroupName,
				"instanceId":       i.Id,
			}).WithError(tErr).Errorf("failed to terminate instance '%s'", *newInstanceId)
		}
		return err
	}
	log.WithFields(log.Fields{
		"autoScalingGroup": i.Asg,
		"instanceId":       i.Id,
		"newInstanceId":    *newInstanceId,
	}).Info("attached instance to auto scaling group")
	_, err = a.AsgSvc.UpdateAutoScalingGroup(&autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: asg.AutoScalingGroupName,
		MinSize:              asg.MinSize,
	})
	if err != nil {
		log.WithFields(log.Fields{
			"autoScalingGroup": *asg.AutoScalingGroupName,
			"instanceId":       i.Id,
		}).WithError(err).Error("couldn't change back min size of auto scaling group")
	}
	log.WithFields(log.Fields{
		"autoScalingGroup": i.Asg,
		"instanceId":       i.Id,
		"newInstanceId":    *newInstanceId,
	}).Infof("changed auto scaling group's min size to %d", *asg.MinSize)
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

	requestSpotInput := ec2.RequestSpotInstancesInput{
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
		},
	}

	if lc.KernelId != nil && *lc.KernelId != "" {
		requestSpotInput.LaunchSpecification.KernelId = lc.KernelId
	}
	if lc.RamdiskId != nil && *lc.RamdiskId != "" {
		requestSpotInput.LaunchSpecification.RamdiskId = lc.RamdiskId
	}
	if lc.UserData != nil && *lc.UserData != "" {
		requestSpotInput.LaunchSpecification.UserData = lc.UserData
	}
	if i.InstanceProfileArn != "" {
		requestSpotInput.LaunchSpecification.IamInstanceProfile = &ec2.IamInstanceProfileSpecification{
			Arn: &i.InstanceProfileArn,
		}
	}
	if asg.PlacementGroup != nil || lc.PlacementTenancy != nil {
		placement := &ec2.SpotPlacement{}
		if asg.PlacementGroup != nil {
			placement.GroupName = asg.PlacementGroup
		}
		if lc.PlacementTenancy != nil {
			placement.Tenancy = lc.PlacementTenancy
		}
		requestSpotInput.LaunchSpecification.Placement = placement
	}

	log.WithFields(log.Fields{
		"autoScalingGroup": *asg.AutoScalingGroupName,
		"instanceId":       i.Id,
	}).Info("requesting spot instance with configuration:", requestSpotInput)

	result, err := a.Ec2Svc.RequestSpotInstances(&requestSpotInput)
	if err != nil {
		return nil, err
	}
	requestId := result.SpotInstanceRequests[0].SpotInstanceRequestId

	log.WithFields(log.Fields{
		"autoScalingGroup": *asg.AutoScalingGroupName,
		"instanceId":       i.Id,
	}).Infof("polling spot instance request '%s' until an instance id is provided", *requestId)
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
			log.WithFields(log.Fields{
				"autoScalingGroup": *asg.AutoScalingGroupName,
				"instanceId":       i.Id,
				"newInstanceId":    *spotRequest.InstanceId,
			}).Infof("polling: instanceId in spot request '%s' is %s", *requestId, *spotRequest.InstanceId)
			instanceId = spotRequest.InstanceId
		} else {
			log.WithFields(log.Fields{
				"autoScalingGroup": *asg.AutoScalingGroupName,
				"instanceId":       i.Id,
			}).Infof("polling: instance id in spot request '%s' is null", *requestId)
		}
		time.Sleep(1 * time.Second)
	}

	// wait until the new instance reaches the running state because only running instances can be attached to an ASG
	log.WithFields(log.Fields{
		"autoScalingGroup": *asg.AutoScalingGroupName,
		"instanceId":       i.Id,
		"newInstanceId":    *instanceId,
	}).Info("polling status of new instance until it's running")
	var running bool
	for !running {
		describeInstResult, err := a.Ec2Svc.DescribeInstanceStatus(&ec2.DescribeInstanceStatusInput{
			InstanceIds: []*string{instanceId},
		})
		if err != nil {
			return nil, err
		}
		if len(describeInstResult.InstanceStatuses) > 0 {
			status := describeInstResult.InstanceStatuses[0]
			log.WithFields(log.Fields{
				"autoScalingGroup": *asg.AutoScalingGroupName,
				"instanceId":       i.Id,
				"newInstanceId":    *instanceId,
			}).Infof("instance is %s", *status.InstanceState.Name)
			if *status.InstanceState.Name == "running" {
				running = true
				continue
			}
		} else {
			log.WithFields(log.Fields{
				"autoScalingGroup": *asg.AutoScalingGroupName,
				"instanceId":       i.Id,
				"newInstanceId":    *instanceId,
			}).Info("polling: instance status is unknown")
		}
		time.Sleep(1 * time.Second)
	}
	return instanceId, nil
}
