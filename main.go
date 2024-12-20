package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/AgoraIO-Community/go-tokenbuilder/rtctokenbuilder"
	"github.com/AgoraIO-Community/go-tokenbuilder/rtmtokenbuilder"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

// AgoraSecrets holds the structure for secrets fetched from AWS Secrets Manager
type AgoraSecrets struct {
	AppID          string `json:"APP_ID"`
	AppCertificate string `json:"APP_CERTIFICATE"`
	BaseURL        string `json:"BASE_URL"`
}

var appID string
var appCertificate string

// FetchAgoraSecrets fetches Agora secrets from AWS Secrets Manager
func FetchAgoraSecrets(secretName string) (*AgoraSecrets, error) {
	// Load the AWS config
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS config: %v", err)
	}

	// Create a Secrets Manager client
	client := secretsmanager.NewFromConfig(cfg)

	// Fetch the secret value
	output, err := client.GetSecretValue(context.TODO(), &secretsmanager.GetSecretValueInput{
		SecretId: &secretName,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to fetch secret: %v", err)
	}

	// Parse the secret value
	var secrets AgoraSecrets
	err = json.Unmarshal([]byte(*output.SecretString), &secrets)
	if err != nil {
		return nil, fmt.Errorf("unable to parse secret: %v", err)
	}
	return &secrets, nil
}

func init() {
	// Load environment variables from .env file (if present)
	if err := godotenv.Load(); err != nil {
		log.Print("No .env file found")
	}
}

func main() {
	// Fetch Agora secrets
	secrets, err := FetchAgoraSecrets("lag-live-agora")
	if err != nil {
		log.Fatalf("Failed to fetch secrets: %v", err)
	}

	// Assign fetched secrets to global variables
	appID = secrets.AppID
	appCertificate = secrets.AppCertificate

	if appID == "" || appCertificate == "" {
		log.Fatal("FATAL ERROR: Secrets not properly configured, check AWS Secrets Manager")
	}

	// Initialize Gin server
	api := gin.Default()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Define routes
	api.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
		})
	})

	api.Use(nocache())
	api.GET("rtc/:channelName/:role/:tokentype/:uid/", getRtcToken)
	api.GET("rtm/:uid/", getRtmToken)
	api.GET("rte/:channelName/:role/:tokentype/:uid/", getBothTokens)

	// Start the server
	api.Run(":" + port)
}

func nocache() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-cache, no-store, must-revalidate")
		c.Header("Expires", "-1")
		c.Header("Pragma", "no-cache")
		c.Header("Access-Control-Allow-Origin", "*")
	}
}

func getRtcToken(c *gin.Context) {
	channelName, tokentype, uidStr, role, expireTimestamp, err := parseRtcParams(c)
	if err != nil {
		c.JSON(400, gin.H{"message": "Error Generating RTC token: " + err.Error()})
		return
	}

	rtcToken, tokenErr := generateRtcToken(channelName, uidStr, tokentype, role, expireTimestamp)
	if tokenErr != nil {
		c.JSON(400, gin.H{"error": "Error Generating RTC token: " + tokenErr.Error()})
	} else {
		c.JSON(200, gin.H{"rtcToken": rtcToken})
	}
}

func getRtmToken(c *gin.Context) {
	uidStr, expireTimestamp, err := parseRtmParams(c)
	if err != nil {
		c.JSON(400, gin.H{"message": "Error Generating RTM token: " + err.Error()})
		return
	}

	rtmToken, tokenErr := rtmtokenbuilder.BuildToken(appID, appCertificate, uidStr, rtmtokenbuilder.RoleRtmUser, expireTimestamp)
	if tokenErr != nil {
		c.JSON(400, gin.H{"error": "Error Generating RTM token: " + tokenErr.Error()})
	} else {
		c.JSON(200, gin.H{"rtmToken": rtmToken})
	}
}

func getBothTokens(c *gin.Context) {
	channelName, tokentype, uidStr, role, expireTimestamp, rtcParamErr := parseRtcParams(c)
	if rtcParamErr != nil {
		c.JSON(400, gin.H{"message": "Error Generating RTC token: " + rtcParamErr.Error()})
		return
	}

	rtcToken, rtcTokenErr := generateRtcToken(channelName, uidStr, tokentype, role, expireTimestamp)
	rtmToken, rtmTokenErr := rtmtokenbuilder.BuildToken(appID, appCertificate, uidStr, rtmtokenbuilder.RoleRtmUser, expireTimestamp)

	if rtcTokenErr != nil {
		c.JSON(400, gin.H{"error": "Error Generating RTC token: " + rtcTokenErr.Error()})
	} else if rtmTokenErr != nil {
		c.JSON(400, gin.H{"error": "Error Generating RTM token: " + rtmTokenErr.Error()})
	} else {
		c.JSON(200, gin.H{"rtcToken": rtcToken, "rtmToken": rtmToken})
	}
}

func parseRtcParams(c *gin.Context) (channelName, tokentype, uidStr string, role rtctokenbuilder.Role, expireTimestamp uint32, err error) {
	channelName = c.Param("channelName")
	roleStr := c.Param("role")
	tokentype = c.Param("tokentype")
	uidStr = c.Param("uid")
	expireTime := c.DefaultQuery("expiry", "3600")

	if roleStr == "publisher" {
		role = rtctokenbuilder.RolePublisher
	} else {
		role = rtctokenbuilder.RoleSubscriber
	}

	expireTime64, parseErr := strconv.ParseUint(expireTime, 10, 64)
	if parseErr != nil {
		err = fmt.Errorf("failed to parse expireTime: %s, causing error: %s", expireTime, parseErr)
	}
	currentTimestamp := uint32(time.Now().UTC().Unix())
	expireTimestamp = uint32(expireTime64) + currentTimestamp
	return
}

func parseRtmParams(c *gin.Context) (uidStr string, expireTimestamp uint32, err error) {
	uidStr = c.Param("uid")
	expireTime := c.DefaultQuery("expiry", "3600")
	expireTime64, parseErr := strconv.ParseUint(expireTime, 10, 64)
	if parseErr != nil {
		err = fmt.Errorf("failed to parse expireTime: %s, causing error: %s", expireTime, parseErr)
	}
	currentTimestamp := uint32(time.Now().UTC().Unix())
	expireTimestamp = uint32(expireTime64) + currentTimestamp
	return
}

func generateRtcToken(channelName, uidStr, tokentype string, role rtctokenbuilder.Role, expireTimestamp uint32) (string, error) {
	if tokentype == "userAccount" {
		return rtctokenbuilder.BuildTokenWithUserAccount(appID, appCertificate, channelName, uidStr, role, expireTimestamp)
	}
	uid, err := strconv.ParseUint(uidStr, 10, 64)
	if err != nil {
		return "", fmt.Errorf("failed to parse uidStr: %v", err)
	}
	return rtctokenbuilder.BuildTokenWithUID(appID, appCertificate, channelName, uint32(uid), role, expireTimestamp)
}
