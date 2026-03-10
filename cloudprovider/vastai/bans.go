package vastai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

func (c *Client) LoadMachineBansFromFile() error {
	if !c.bansConfig.Enable {
		return nil
	}
	path := c.bansConfig.FilePath
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var bans map[string]time.Time
	if err := json.Unmarshal(data, &bans); err != nil {
		return err
	}
	c.bansMu.Lock()
	defer c.bansMu.Unlock()
	for k, v := range bans {
		c.machineBans[k] = v
	}
	return nil
}

// persistMachineBansToFile must be called with bansMu held.
func (c *Client) persistMachineBansToFile() error {
	if !c.bansConfig.Enable {
		return nil
	}
	path := c.bansConfig.FilePath
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	bansCopy := make(map[string]time.Time, len(c.machineBans))
	for k, v := range c.machineBans {
		bansCopy[k] = v
	}
	data, err := json.MarshalIndent(bansCopy, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
