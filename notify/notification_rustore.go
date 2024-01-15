package notify

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/appleboy/gorush/config"
	"github.com/appleboy/gorush/core"
	"github.com/appleboy/gorush/logx"
	"github.com/appleboy/gorush/status"
	"golang.org/x/net/http2"
)

type RustoreResponse struct {
	MessageID string `json:"message_id"`
	Error     string `json:"error"`
}

type RustoreMessage struct {
	Message RustoreMessageContent `json:"message"`
}

type RustoreMessageContent struct {
	Token        string        `json:"token"`
	Data         D             `json:"data"`
	Notification *Notification `json:"notification,omitempty"`
	Android      *Android      `json:"android,omitempty"`
}

type Notification struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
	Image string `json:"image,omitempty"`
}

type Android struct {
	TTL          string               `json:"ttl,omitempty"`
	Notification *RustoreNotification `json:"notification"`
}

type RustoreNotification struct {
	Title       string `json:"title,omitempty"`
	Body        string `json:"body,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Color       string `json:"color,omitempty"`
	Image       string `json:"image,omitempty"`
	ChannelId   string `json:"channel_id,omitempty"`
	ClickAction string `json:"click_action,omitempty"`
}

type RustoreClient struct {
	endpoint     string
	client       *http.Client
	timeout      time.Duration
	serviceToken string
}

var doItOnce sync.Once

func InitRustoreClient(cfg *config.ConfYaml, serviceToken string, projectId string) (*RustoreClient, error) {
	if serviceToken == "" && cfg.Rustore.ServiceToken == "" {
		return nil, errors.New("missing rustore service token")
	}
	if projectId == "" && cfg.Rustore.ProjectID == "" {
		return nil, errors.New("missing rustore project id")
	}

	doItOnce.Do(func() {
		MaxConcurrentRustorePushes = make(chan struct{}, cfg.Rustore.MaxConcurrentPushes)
	})

	var pId string
	if projectId != "" && projectId != cfg.Rustore.ProjectID {
		pId = projectId
	} else {
		pId = cfg.Rustore.ProjectID
	}
	url := fmt.Sprintf("https://vkpns.rustore.ru/v1/projects/%s/messages:send", pId)

	if serviceToken != "" && serviceToken != cfg.Rustore.ServiceToken {
		return newClient(url, serviceToken), nil
	}

	if RSClient == nil {
		RSClient = newClient(url, cfg.Rustore.ServiceToken)
		return RSClient, nil
	}

	return RSClient, nil
}

func newClient(url string, serviceToken string) *RustoreClient {
	client := &http.Client{}
	client.Transport = &http2.Transport{}
	return &RustoreClient{
		endpoint:     url,
		client:       client,
		timeout:      30 * time.Second,
		serviceToken: serviceToken,
	}
}

func prepareRustoreMessage(req *PushNotification) *RustoreMessage {
	msg := &RustoreMessage{}
	rustNoti := &Notification{
		Title: req.Title,
		Body:  req.Message,
		Image: req.Image,
	}
	if len(req.Data) > 0 {
		msg.Message.Data = make(map[string]interface{})
		for k, v := range req.Data {
			// сonvert value to string cause rustore do not support ints in data
			switch v := v.(type) {
			case int:
				msg.Message.Data[k] = fmt.Sprintf("%d", v)
			default:
				msg.Message.Data[k] = v
			}
		}
		logx.LogAccess.Debug("Data:")
		logx.LogAccess.Debug(msg.Message.Data)
		if req.Data["title"] == "" && req.Title != "" {
			msg.Message.Data["title"] = req.Title
		}
		if req.Data["message"] == "" && req.Message != "" {
			msg.Message.Data["message"] = req.Message
		}
	}
	msg.Message.Notification = rustNoti
	return msg
}

func PushToRustore(noti *PushNotification, cfg *config.ConfYaml) (resp *ResponsePush, err error) {
	logx.LogAccess.Debug("Start push notifications for Rustore")

	var (
		rustoreClient *RustoreClient
		retryCount    = 0
		maxRetry      = cfg.Rustore.MaxRetry
	)

	if noti.Retry > 0 && noti.Retry < maxRetry {
		maxRetry = noti.Retry
	}

	if noti.ServiceToken == "" && cfg.Rustore.ServiceToken == "" {
		logx.LogError.Error("request error: Missing rustore service token")
		return
	}

	if noti.ProjectID == "" && cfg.Rustore.ProjectID == "" {
		logx.LogError.Error("request error: Missing rustore project_id")
		return
	}

	err = CheckMessage(noti)
	if err != nil {
		logx.LogError.Error("request error: " + err.Error())
		return
	}

	resp = &ResponsePush{}

Retry:
	var newTokens []string

	if noti.ServiceToken != "" && noti.ProjectID != "" {
		rustoreClient, err = InitRustoreClient(cfg, noti.ServiceToken, noti.ProjectID)
	} else {
		rustoreClient, err = InitRustoreClient(cfg, "", "")
	}

	if err != nil {
		logx.LogError.Error("Rustore client error: " + err.Error())
		return
	}
	message := prepareRustoreMessage(noti)

	var wg sync.WaitGroup
	for _, token := range noti.Tokens {
		MaxConcurrentRustorePushes <- struct{}{}
		wg.Add(1)
		func(msg RustoreMessage, token string) {
			defer func() {
				<-MaxConcurrentRustorePushes
				wg.Done()
			}()

			msg.Message.Token = token
			body, json_err := json.Marshal(msg)
			if json_err != nil {
				errLog := logPush(cfg, core.FailedPush, token, noti, json_err)
				resp.Logs = append(resp.Logs, errLog)
				return
			}

			req, req_err := http.NewRequest("POST", rustoreClient.endpoint, bytes.NewBuffer(body))
			if req_err != nil {
				errLog := logPush(cfg, core.FailedPush, token, noti, req_err)
				resp.Logs = append(resp.Logs, errLog)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			if noti.ServiceToken != "" {
				req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", noti.ServiceToken))
			} else {
				req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.Rustore.ServiceToken))
			}

			// Sending req to Rustore
			response, err := rustoreClient.client.Do(req)
			if err != nil || (response != nil && response.StatusCode != http.StatusOK) {
				if err != nil {
					logx.LogAccess.Debug(err.Error())
				}

				errLog := logPush(cfg, core.FailedPush, token, noti, err)
				resp.Logs = append(resp.Logs, errLog)

				status.StatStorage.AddRustoreError(1)

				if response != nil && response.StatusCode >= http.StatusInternalServerError {
					newTokens = append(newTokens, token)
				}
			}
			defer response.Body.Close()

			if response != nil && response.StatusCode == http.StatusOK {
				logPush(cfg, core.SucceededPush, token, noti, nil)
				status.StatStorage.AddRustoreSuccess(1)
			}
		}(*message, token)
	}

	wg.Wait()

	if len(newTokens) > 0 && retryCount < maxRetry {
		retryCount++
		noti.Tokens = newTokens
		goto Retry
	}

	return resp, nil
}
