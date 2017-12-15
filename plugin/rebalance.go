package plugin

import (
	"errors"
	"strings"

	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/banzaicloud/hollowtrees/recommender"
	"github.com/sirupsen/logrus"
)

func rebalanceASG(session *session.Session, name string) error {
	log.WithFields(logrus.Fields{
		"autoScalingGroup": name,
	}).Info("ASG will be rebalanced: ", name)
	ec2Svc := ec2.New(session, aws.NewConfig())
	asgSvc := autoscaling.New(session, aws.NewConfig())
	describeResult, err := asgSvc.DescribeAutoScalingGroups(&autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{&name},
	})
	if err != nil {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": name,
		}).Error("Couldn't describe AutoScaling Groups: " + err.Error())
		return err
	}
	group := describeResult.AutoScalingGroups[0]

	var instanceIds []*string
	if len(group.Instances) > 0 {
		for _, instance := range group.Instances {
			instanceIds = append(instanceIds, instance.InstanceId)
		}
	}

	state, err := getCurrentInstanceTypeState(ec2Svc, *group.AutoScalingGroupName, instanceIds)
	if err != nil {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": name,
		}).Error(err.Error())
		return err
	}

	subnetIds := strings.Split(*group.VPCZoneIdentifier, ",")
	subnets, err := ec2Svc.DescribeSubnets(&ec2.DescribeSubnetsInput{
		SubnetIds: aws.StringSlice(subnetIds),
	})
	if err != nil {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": name,
		}).Error("couldn't describe subnets" + err.Error())
		return err
	}

	subnetsPerAz := make(map[string][]string)
	for _, subnet := range subnets.Subnets {
		subnetsPerAz[*subnet.AvailabilityZone] = append(subnetsPerAz[*subnet.AvailabilityZone], *subnet.SubnetId)
	}

	azList := make([]string, 0, len(subnetsPerAz))
	for k := range subnetsPerAz {
		azList = append(azList, k)
	}

	baseInstanceType, err := findBaseInstanceType(asgSvc, *group.AutoScalingGroupName, *group.LaunchConfigurationName)
	if err != nil {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": name,
		}).Error("couldn't find base instance type")
		return err
	}
	recommendations, err := recommender.RecommendSpotInstanceTypes(*session.Config.Region, azList, baseInstanceType)
	if err != nil {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": name,
		}).Error("couldn't get recommendations")
		return err
	}

	for stateInfo, instanceIdsOfType := range state {
		recommendationContains := false
		for _, recommendation := range recommendations[stateInfo.az] {
			if stateInfo.spotBidPrice != "" && recommendation.InstanceTypeName == stateInfo.instType {
				recommendationContains = true
				break
			}
		}
		if !recommendationContains {
			log.WithFields(logrus.Fields{
				"autoScalingGroup": name,
			}).Info("this instance type will be changed to a different one because it is not among the recommended options:", stateInfo)

			launchConfigs, err := asgSvc.DescribeLaunchConfigurations(&autoscaling.DescribeLaunchConfigurationsInput{
				LaunchConfigurationNames: []*string{group.LaunchConfigurationName},
			})
			if err != nil {
				log.WithFields(logrus.Fields{
					"autoScalingGroup": name,
				}).Error("something happened during describing launch configs" + err.Error())
				return err
			}

			// TODO: we should check the current diversification of the ASG and set the selected instance types accordingly
			// TODO: this way it's possible that if there's 100 instances in AZ-1a, 25 of them is of type A and no longer recommended
			// TODO: selectInstanceTypesByCost will select 4 different types because it's 25

			selectedInstanceTypes := make(map[string][]recommender.InstanceTypeInfo)
			selectedInstanceTypes[stateInfo.az] = selectInstanceTypesByCost(recommendations[stateInfo.az], int64(len(instanceIdsOfType)))
			countsPerAz := make(map[string]int64)
			countsPerAz[stateInfo.az] = int64(len(instanceIdsOfType))

			instanceIdsToAttach, err := requestAndWaitSpotInstances(ec2Svc, name, countsPerAz, subnetsPerAz, selectedInstanceTypes, *launchConfigs.LaunchConfigurations[0])

			// change ASG min size so we can detach instances
			_, err = asgSvc.UpdateAutoScalingGroup(&autoscaling.UpdateAutoScalingGroupInput{
				AutoScalingGroupName: group.AutoScalingGroupName,
				MinSize:              aws.Int64(int64(len(instanceIds) - len(instanceIdsOfType))),
			})
			if err != nil {
				log.WithFields(logrus.Fields{
					"autoScalingGroup": name,
				}).Error("failed to update ASG: ", err.Error())
				return err
			}

			// TODO: we shouldn't detach all instances at once, we can stick to the minsize of the group and only detach
			// TODO: as many instances as we can until the minsize, then start it again
			_, err = asgSvc.DetachInstances(&autoscaling.DetachInstancesInput{
				AutoScalingGroupName:           group.AutoScalingGroupName,
				ShouldDecrementDesiredCapacity: aws.Bool(true),
				InstanceIds:                    instanceIdsOfType,
			})
			if err != nil {
				log.WithFields(logrus.Fields{
					"autoScalingGroup": name,
				}).Error("failed to detach instances: ", err.Error())
				return err
			}

			_, err = ec2Svc.TerminateInstances(&ec2.TerminateInstancesInput{
				InstanceIds: instanceIdsOfType,
			})
			if err != nil {
				log.WithFields(logrus.Fields{
					"autoScalingGroup": name,
				}).Error("failed to terminate instances: ", err.Error())
				return err
			}

			_, err = asgSvc.AttachInstances(&autoscaling.AttachInstancesInput{
				InstanceIds:          instanceIdsToAttach,
				AutoScalingGroupName: group.AutoScalingGroupName,
			})
			if err != nil {
				log.WithFields(logrus.Fields{
					"autoScalingGroup": name,
				}).Error("failed to attach instances: ", err.Error())
				return err
			}

			// change back ASG min size to original
			_, err = asgSvc.UpdateAutoScalingGroup(&autoscaling.UpdateAutoScalingGroupInput{
				AutoScalingGroupName: group.AutoScalingGroupName,
				MinSize:              group.MinSize,
			})
			if err != nil {
				log.WithFields(logrus.Fields{
					"autoScalingGroup": name,
				}).Error("couldn't update min size", err.Error())
				return err
			}

			// wait until there are no pending instances in ASG
			nrOfPending := len(instanceIdsOfType)
			for nrOfPending != 0 {
				nrOfPending = 0
				r, err := asgSvc.DescribeAutoScalingGroups(&autoscaling.DescribeAutoScalingGroupsInput{
					AutoScalingGroupNames: []*string{group.AutoScalingGroupName},
				})
				if err != nil {
					log.WithFields(logrus.Fields{
						"autoScalingGroup": name,
					}).Error("couldn't describe ASG")
					return err
				}
				for _, instance := range r.AutoScalingGroups[0].Instances {
					if *instance.LifecycleState == "Pending" {
						log.WithFields(logrus.Fields{
							"autoScalingGroup": name,
						}).Info("found a pending instance: ", *instance.InstanceId)
						nrOfPending++
					}
				}
				time.Sleep(1 * time.Second)
			}
		}
	}
	return nil
}

type InstanceType struct {
	instType     string
	az           string
	spotBidPrice string
}

type InstanceTypes map[InstanceType][]*string

func getCurrentInstanceTypeState(ec2Svc *ec2.EC2, asgName string, instanceIds []*string) (InstanceTypes, error) {
	if len(instanceIds) < 1 {
		return nil, errors.New("number of instance ids cannot be less than 1")
	}
	instances, err := ec2Svc.DescribeInstances(&ec2.DescribeInstancesInput{
		InstanceIds: instanceIds,
	})
	if err != nil {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": asgName,
		}).Error("Failed to describe instances in AutoScaling Group: ", err.Error())
		return nil, err
	}

	state := make(InstanceTypes)

	var spotRequests []*string
	for _, reservation := range instances.Reservations {
		for _, instance := range reservation.Instances {
			if instance.SpotInstanceRequestId != nil {
				spotRequests = append(spotRequests, instance.SpotInstanceRequestId)
			} else {
				it := InstanceType{
					instType:     *instance.InstanceType,
					az:           *instance.Placement.AvailabilityZone,
					spotBidPrice: "",
				}
				state[it] = append(state[it], instance.InstanceId)
			}
		}
	}
	if len(spotRequests) > 0 {
		output, err := ec2Svc.DescribeSpotInstanceRequests(&ec2.DescribeSpotInstanceRequestsInput{
			SpotInstanceRequestIds: spotRequests,
		})
		if err != nil {
			log.WithFields(logrus.Fields{
				"autoScalingGroup": asgName,
			}).Error("Failed to describe spot requests: ", err.Error())
			return nil, err
		}

		for _, spotRequest := range output.SpotInstanceRequests {
			it := InstanceType{
				instType:     *spotRequest.LaunchSpecification.InstanceType,
				az:           *spotRequest.LaunchedAvailabilityZone,
				spotBidPrice: *spotRequest.SpotPrice,
			}
			state[it] = append(state[it], spotRequest.InstanceId)
		}
	}
	log.WithFields(logrus.Fields{
		"autoScalingGroup": asgName,
	}).Info("current state of instanceTypes in ASG: ", state)
	return state, err
}

func findBaseInstanceType(asgSvc *autoscaling.AutoScaling, asgName string, lcName string) (string, error) {
	originalLCName := asgName + "-ht-orig"
	originalLaunchConfigs, err := asgSvc.DescribeLaunchConfigurations(&autoscaling.DescribeLaunchConfigurationsInput{
		LaunchConfigurationNames: []*string{&originalLCName},
	})
	if err != nil {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": asgName,
		}).Error("Failed to describe launch configurations: ", err.Error())
		return "", err
	}
	log.WithFields(logrus.Fields{
		"autoScalingGroup": asgName,
	}).Info("Described original LaunchConfigs, length of result is: ", len(originalLaunchConfigs.LaunchConfigurations))

	if len(originalLaunchConfigs.LaunchConfigurations) > 0 {
		log.WithFields(logrus.Fields{
			"autoScalingGroup": asgName,
		}).Info("Base instance type is: ", *originalLaunchConfigs.LaunchConfigurations[0].InstanceType)
		return *originalLaunchConfigs.LaunchConfigurations[0].InstanceType, nil
	} else {
		launchConfigs, err := asgSvc.DescribeLaunchConfigurations(&autoscaling.DescribeLaunchConfigurationsInput{
			LaunchConfigurationNames: []*string{&lcName},
		})
		if err != nil {
			log.WithFields(logrus.Fields{
				"autoScalingGroup": asgName,
			}).Error("something happened during describing launch configs" + err.Error())
			return "", err
		}
		log.WithFields(logrus.Fields{
			"autoScalingGroup": asgName,
		}).Info("Base instance type is: ", *launchConfigs.LaunchConfigurations[0].InstanceType)
		return *launchConfigs.LaunchConfigurations[0].InstanceType, nil
	}
}
