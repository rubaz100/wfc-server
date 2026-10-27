package gpcm

import (
	"os"
)

var motdFilepath = "./motd.txt"

func GetMessageOfTheDay() (string, error) {
	contents, err := os.ReadFile(motdFilepath)
	if err != nil {
		return "", err
	}

	return string(contents), nil
}

// SetMessageOfTheDay writes the message of the day file. It takes effect
// immediately since GetMessageOfTheDay reads the file on each call.
func SetMessageOfTheDay(text string) error {
	return os.WriteFile(motdFilepath, []byte(text), 0644)
}

