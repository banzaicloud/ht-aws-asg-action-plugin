## Action plugin to manipulate AWS auto-scaling groups

Action plugins are `reaction` implementations based on alerts raised by [Hollowtrees](https://github.com/banzaicloud/hollowtrees) to perform operator desired actions. They are invoked thorugh **gRPC** from the Hollowtrees system and follow this [proto])https://github.com/banzaicloud/hollowtrees/blob/master/action/action.proto) file implementation.

### Instance lifecycle in ASG

Auto Scaling Groups helps to ensure that the correct number of Amazon EC2 instances are available to handle the load for the application. It can specify the minimum and maximum number of instances in each Auto Scaling group, and Auto Scaling ensures that the group never goes below or above this size. However, when working with spot instances ASG have limitations which the plugin overcomes and deales with.

* mixing different instance types inside AGS
* follows recommendations by [Hollowtrees](https://github.com/banzaicloud/hollowtrees) to replace or add/remove instances from ASG


### ht-aws-asg-action-plugin actions

Based on external events the plugin execute the following actions:

* upscale
* downscale 
* rebalance
