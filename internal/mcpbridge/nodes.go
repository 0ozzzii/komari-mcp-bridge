package mcpbridge

import (
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/models"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	agent "github.com/komari-monitor/komari/web/agent"
)

// The picker uses the same client list, weight and presence sources as the
// native panel. It needs neither probe credentials nor a second polling socket.
type pickerNode struct {
	UUID     string     `json:"uuid"`
	Name     string     `json:"name"`
	Region   string     `json:"region"`
	Weight   int        `json:"weight"`
	Online   bool       `json:"online"`
	LastSeen *time.Time `json:"last_seen"`
}

func listNodes(c *gin.Context) {
	input, err := clients.GetAllClientBasicInfo()
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "node listing failed"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, pickerNodes(input, agent.GetAllOnlineUUIDs(), agent.GetLatestReport()))
}

func pickerNodes(input []models.Client, online []string, reports map[string]*v2.Report) []pickerNode {
	onlineSet := make(map[string]bool, len(online))
	for _, uuid := range online {
		onlineSet[uuid] = true
	}
	output := make([]pickerNode, 0, len(input))
	for _, client := range input {
		node := pickerNode{UUID: client.UUID, Name: client.Name, Region: client.Region, Weight: client.Weight, Online: onlineSet[client.UUID]}
		if report := reports[client.UUID]; report != nil && !report.UpdatedAt.IsZero() {
			timestamp := report.UpdatedAt.UTC()
			node.LastSeen = &timestamp
		}
		output = append(output, node)
	}
	// Match the server-list stable weight sort, including its tie ordering.
	sort.SliceStable(output, func(i, j int) bool { return output[i].Weight < output[j].Weight })
	return output
}
