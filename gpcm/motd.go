package gpcm

import (
	"fmt"
	"os"
	"wwfc/common"
)

func GetMessageOfTheDay() (string, error) {
	contents, err := os.ReadFile(common.DataPath("motd.txt"))
	if err != nil {
		return "", err
	}

	return string(contents), nil
}

// SetMessageOfTheDay writes the message of the day file. It takes effect
// immediately since GetMessageOfTheDay reads the file on each call.
func SetMessageOfTheDay(text string) error {
	if len(text) > 8192 {
		return fmt.Errorf("message of the day exceeds 8192 bytes")
	}
	path := common.DataPath("motd.txt")
	temporary, err := os.CreateTemp(common.DataPath(), ".motd-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(text); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}
