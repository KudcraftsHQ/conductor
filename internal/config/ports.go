package config

import (
	"fmt"
	"sort"
	"strconv"
)

// AllocatePorts finds and allocates N consecutive free ports
func (c *Config) AllocatePorts(project, worktree string, count int) ([]int, error) {
	if count <= 0 {
		return nil, fmt.Errorf("port count must be positive")
	}

	// Get all used ports as a sorted slice
	usedPorts := c.getUsedPorts()

	// Find first gap of `count` consecutive ports
	ports := c.findConsecutivePorts(usedPorts, count)
	if ports == nil {
		return nil, fmt.Errorf("no %d consecutive ports available in range %d-%d",
			count, c.Defaults.PortRangeStart, c.Defaults.PortRangeEnd)
	}

	// Allocate the ports
	for i, port := range ports {
		c.PortAllocations[strconv.Itoa(port)] = &PortAlloc{
			Project:  project,
			Worktree: worktree,
			Index:    i,
		}
	}

	return ports, nil
}

// FreePorts removes port allocations
func (c *Config) FreePorts(ports []int) {
	for _, port := range ports {
		delete(c.PortAllocations, strconv.Itoa(port))
	}
}

// FreeWorktreePorts removes all ports for a worktree
func (c *Config) FreeWorktreePorts(project, worktree string) {
	toDelete := []string{}
	for portStr, alloc := range c.PortAllocations {
		if alloc.Project == project && alloc.Worktree == worktree {
			toDelete = append(toDelete, portStr)
		}
	}
	for _, portStr := range toDelete {
		delete(c.PortAllocations, portStr)
	}
}

// GetProjectPorts returns all ports allocated to a project
func (c *Config) GetProjectPorts(project string) []int {
	var ports []int
	for portStr, alloc := range c.PortAllocations {
		if alloc.Project == project {
			port, _ := strconv.Atoi(portStr)
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)
	return ports
}

// GetWorktreePorts returns all ports allocated to a worktree
func (c *Config) GetWorktreePorts(project, worktree string) []int {
	var ports []int
	for portStr, alloc := range c.PortAllocations {
		if alloc.Project == project && alloc.Worktree == worktree {
			port, _ := strconv.Atoi(portStr)
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)
	return ports
}

// IsPortAvailable checks if a specific port is free
func (c *Config) IsPortAvailable(port int) bool {
	_, exists := c.PortAllocations[strconv.Itoa(port)]
	return !exists && port >= c.Defaults.PortRangeStart && port <= c.Defaults.PortRangeEnd
}

// TotalUsedPorts returns the count of allocated ports
func (c *Config) TotalUsedPorts() int {
	return len(c.PortAllocations)
}

// getUsedPorts returns a sorted slice of all used ports
func (c *Config) getUsedPorts() []int {
	ports := make([]int, 0, len(c.PortAllocations))
	for portStr := range c.PortAllocations {
		port, _ := strconv.Atoi(portStr)
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}

// findConsecutivePorts finds the first gap of N consecutive ports
func (c *Config) findConsecutivePorts(usedPorts []int, count int) []int {
	start := c.Defaults.PortRangeStart
	end := c.Defaults.PortRangeEnd

	// Create a set for O(1) lookup
	usedSet := make(map[int]bool)
	for _, p := range usedPorts {
		usedSet[p] = true
	}

	// Scan for consecutive free ports
	for port := start; port <= end-count+1; port++ {
		found := true
		for i := 0; i < count; i++ {
			if usedSet[port+i] {
				found = false
				// Skip to after the used port
				port = port + i
				break
			}
		}
		if found {
			ports := make([]int, count)
			for i := 0; i < count; i++ {
				ports[i] = port + i
			}
			return ports
		}
	}

	return nil
}

// PortInfo contains information about a port allocation
type PortInfo struct {
	Port     int
	Project  string
	Worktree string
	Index    int
	Label    string
}

// GetAllPortInfo returns detailed info about all allocated ports
func (c *Config) GetAllPortInfo() []PortInfo {
	var info []PortInfo
	for portStr, alloc := range c.PortAllocations {
		port, _ := strconv.Atoi(portStr)

		// Try to get label from project config
		label := ""
		if proj, ok := c.Projects[alloc.Project]; ok {
			projCfg, err := LoadProjectConfig(proj.Path)
			if err == nil && projCfg != nil && alloc.Index < len(projCfg.Ports.Labels) {
				label = projCfg.Ports.Labels[alloc.Index]
			}
		}

		info = append(info, PortInfo{
			Port:     port,
			Project:  alloc.Project,
			Worktree: alloc.Worktree,
			Index:    alloc.Index,
			Label:    label,
		})
	}

	// Sort by port number
	sort.Slice(info, func(i, j int) bool {
		return info[i].Port < info[j].Port
	})

	return info
}
