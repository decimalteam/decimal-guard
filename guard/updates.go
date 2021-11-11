package guard

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"os"
	"sync"
)

var (
	UpdateInfo = NewUpdateInfo("guard.update_block.json")
)

type updateInfo struct {
	filename    string
	mutex       sync.Mutex
	UpdateBlock int64 `json:"update_block"`
}

func NewUpdateInfo(planfile string) *updateInfo {
	return &updateInfo{
		filename:    planfile,
		UpdateBlock: -1,
	}
}

func (plan *updateInfo) Check() error {
	err := plan.Load()
	if err != nil {
		return err
	}

	if plan.UpdateBlock != -1 {
		return errors.New("failed to check update info file")
	}

	return nil
}

func (plan *updateInfo) Push(height int64) error {
	plan.mutex.Lock()
	defer plan.mutex.Unlock()

	plan.UpdateBlock = height

	bytes, err := json.Marshal(plan)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(plan.filename, bytes, 0644)
	if err != nil {
		return err
	}

	return nil
}

func (plan *updateInfo) Load() error {
	plan.mutex.Lock()
	defer plan.mutex.Unlock()

	if !fileExist(plan.filename) {
		err := ioutil.WriteFile(plan.filename, []byte("{}"), 0644)
		if err != nil {
			return err
		}
	}

	bytes, err := ioutil.ReadFile(plan.filename)
	if err != nil {
		return err
	}

	err = json.Unmarshal(bytes, plan)
	if err != nil {
		return err
	}

	return nil
}

func fileExist(filename string) bool {
	_, err := os.Stat(filename)
	return !os.IsNotExist(err)
}
