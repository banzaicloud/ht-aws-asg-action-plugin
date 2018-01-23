### AWS Autoscaling Hollowtrees action plugin

This is an action plugin for the [Hollowtrees](https://github.com/banzaicloud/hollowtrees) project.
It interacts with AWS auto scaling groups by changing instances to new ones with better cost or stability characteristics.

When started it is listening on a GRPC interface and accepts Hollowtrees events.

### Quick start

Building the project is as simple as running a go build command. The result is a statically linked executable binary.
```
go build .
```

### Configuration

The following options can be configured when starting the action plugin. Configuration is done through a `plugin-config.toml` file that can be placed in `conf/` or near the binary:

```
[log]
    format = "text"
    level = "debug"

[plugin]
    port = "8888"
    region = "eu-west-1"
```

The project is using the standard aws go client library, so AWS credentials can be provided through env variables, instance profiles or config files in the `~/.aws` directory.

To run:
```
./ht-aws-asg-action-plugin
```

### Event types that the plugin can understand:

`prometheus.server.alert.SpotTerminationNotice` - detaches the AWS instance from the auto scaling group that will be terminated, and starts a new instance with the same characteristics but with a different instance type and spot bid price that will be attached to the auto scaling group instead.

`prometheus.server.alert.SpotInstanceTooExpensive` - same as above but terminates the instance after detaching it from the auto-scaling group. Can be used to change "expensive" instances to other instance types in an auto scaling group.