package notify

import (
	"github.com/appleboy/go-fcm"
	"github.com/msalihkarakasli/go-hms-push/push/core"
	"github.com/sideshow/apns2"
)

var (
	// ApnsClient is apns client
	ApnsClient *apns2.Client
	// FCMClient is apns client
	FCMClient *fcm.Client
	// HMSClient is Huawei push client
	HMSClient *core.HMSClient
	// RustoreClient is obviously is rustore push client
	RSClient *RustoreClient
	// MaxConcurrentRustorePushes pool to limit num of concurrent Rustore pushes
	MaxConcurrentRustorePushes chan struct{}
	// MaxConcurrentIOSPushes pool to limit the number of concurrent iOS pushes
	MaxConcurrentIOSPushes chan struct{}
)

const (
	HIGH   = "high"
	NORMAL = "nornal"
)
