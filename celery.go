package main

import (
	"fmt"
	"github.com/yabamuro/gocelery"
)

// Stripe Interface below
type celeryObj struct {
	taskName    string
	notifyTaskName string
	celeryClient    *gocelery.CeleryClient
	notifyClient    *gocelery.CeleryClient
}

type celeryJob struct {
	celeryManager celeryManager
}

func newCelery(taskName, notifyTaskName string, celeryClient, notifyClient *gocelery.CeleryClient) celeryManager {
	if taskName == "" || notifyTaskName == "" {
		logger.Error("taskName or notifyTaskName is empty.")
	}
	cel := &celeryObj{taskName, notifyTaskName, celeryClient, notifyClient}
	return cel
}

type celeryManager interface {
	delay(string, *Requestparam) error
	nDelay(string, *Requestparam) error
}

func (c *celeryObj) delay(taskName string, rp *Requestparam) error {
	_, err := c.celeryClient.Delay(taskName, rp)
	if err != nil {
		return err
	}
	return nil
}

func (c *celeryObj) nDelay(notifyTaskName string, rp *Requestparam) error {
	_, err := c.notifyClient.Delay(notifyTaskName, rp.Address, fmt.Sprintf("The 'ProductName:[%v]' has been purchased.", rp.ProductName))
	if err != nil {
		return err
	}
	return nil
}
