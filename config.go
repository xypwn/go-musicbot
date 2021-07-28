package main

import (
	"encoding/json"
	"errors"
	"os"
)

type Config struct {
	Prefix     string `json:"prefix"`
	Token      string `json:"token"`
	YtdlPath   string `json:"youtube-dl_path"`
	FfmpegPath string `json:"ffmpeg_path"`
}

const configFile = "config.json"

const tokenDefaultString = "insert your discord bot token here"

func ReadConfig(cfg *Config) error {
	configData, err := os.ReadFile(configFile)
	if err != nil {
		return errors.New("unable to read config file: " + err.Error())
	}

	json.Unmarshal(configData, cfg)
	if err != nil {
		return errors.New("unable to decode config file: " + err.Error())
	}
	return nil
}

func WriteDefaultConfig() error {
	data, err := json.MarshalIndent(Config{
		Prefix:     "!",
		Token:      tokenDefaultString,
		YtdlPath:   "youtube-dl",
		FfmpegPath: "ffmpeg",
	}, "", "\t")
	if err != nil {
		return err
	}

	return os.WriteFile(configFile, data, 0666)
}
