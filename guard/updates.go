package guard

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"sync"
)

const (
	OneHour = 660
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

func (plan *updateInfo) Push(height int64) {
	plan.mutex.Lock()
	defer plan.mutex.Unlock()

	plan.UpdateBlock = height

	bytes, err := json.Marshal(plan)
	if err != nil {
		panic(err)
	}

	err = ioutil.WriteFile(plan.filename, bytes, 0644)
	if err != nil {
		panic(err)
	}
}

func (plan *updateInfo) Load() int64 {
	plan.mutex.Lock()
	defer plan.mutex.Unlock()

	if !fileExist(plan.filename) {
		err := ioutil.WriteFile(plan.filename, []byte("{}"), 0600)
		if err != nil {
			panic(err)
		}
	}

	bytes, err := ioutil.ReadFile(plan.filename)
	if err != nil {
		panic(err)
	}

	err = json.Unmarshal(bytes, plan)
	if err != nil {
		panic(err)
	}

	return plan.UpdateBlock
}

func fileExist(filename string) bool {
	_, err := os.Stat(filename)
	return !os.IsNotExist(err)
}
