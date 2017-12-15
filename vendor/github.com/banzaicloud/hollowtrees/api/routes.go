package api

import (
	"fmt"
	"net/http"

	"strings"

	"github.com/banzaicloud/hollowtrees/conf"
	"github.com/banzaicloud/hollowtrees/engine"
	"github.com/banzaicloud/hollowtrees/engine/aws"
	"github.com/banzaicloud/hollowtrees/recommender"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gopkg.in/go-playground/validator.v8"
)

var log *logrus.Entry

func init() {
	log = conf.Logger().WithField("package", "api")
}

func ConfigureRoutes(router *gin.Engine) {
	log.Info("configuring routes")
	v1 := router.Group("/api/v1/")
	{
		v1.GET("/recommender/:region", recommendSpotInstanceTypes)
		v1.POST("/hollowgroups", createHollowGroup)
	}
}

func recommendSpotInstanceTypes(c *gin.Context) {
	log.Info("recommend spot instance types")
	region := c.Param("region")
	baseInstanceType := c.DefaultQuery("baseInstanceType", "m4.xlarge")
	azsQuery := c.DefaultQuery("availabilityZones", "")
	var azs []string
	if azsQuery == "" {
		azs = nil
	} else {
		azs = strings.Split(azsQuery, ",")
	}
	if response, err := recommender.RecommendSpotInstanceTypes(region, azs, baseInstanceType); err != nil {
		// TODO: handle different error types
		c.JSON(http.StatusInternalServerError, gin.H{"status": http.StatusInternalServerError, "message": fmt.Sprintf("%s", err)})
	} else {
		c.JSON(http.StatusOK, gin.H{"status": http.StatusOK, "data": response})
	}
}

func createHollowGroup(c *gin.Context) {
	log.Info("create VM pool")
	hgRequest := new(engine.HollowGroupRequest)
	if err := c.BindJSON(hgRequest); err != nil {
		if ve, ok := err.(validator.ValidationErrors); !ok {
			// TODO: not a validation error
			fmt.Println(err)
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"status": http.StatusBadRequest, "message": "Missing required field", "error": ve.Error()})
		}
		return
	}
	awsEngine, err := aws.New("eu-west-1")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": http.StatusInternalServerError, "message": fmt.Sprintf("%s", err)})
	}
	if response, err := awsEngine.CreateHollowGroup(hgRequest); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": http.StatusInternalServerError, "message": fmt.Sprintf("%s", err)})
	} else {
		c.JSON(http.StatusOK, gin.H{"status": http.StatusOK, "data": response})
	}
}
