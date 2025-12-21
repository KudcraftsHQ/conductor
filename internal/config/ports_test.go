package config

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllocatePorts_EmptyConfig(t *testing.T) {
	cfg := NewConfig()

	ports, err := cfg.AllocatePorts("myproject", "main", 3)

	require.NoError(t, err)
	assert.Equal(t, []int{3100, 3101, 3102}, ports)
	assert.Equal(t, 3, len(cfg.PortAllocations))

	// Verify allocations are recorded correctly
	alloc := cfg.PortAllocations["3100"]
	assert.Equal(t, "myproject", alloc.Project)
	assert.Equal(t, "main", alloc.Worktree)
	assert.Equal(t, 0, alloc.Index)
}

func TestAllocatePorts_WithExistingAllocations(t *testing.T) {
	cfg := NewConfig()

	// Allocate first set
	ports1, err := cfg.AllocatePorts("project1", "wt1", 2)
	require.NoError(t, err)
	assert.Equal(t, []int{3100, 3101}, ports1)

	// Allocate second set - should continue from where first left off
	ports2, err := cfg.AllocatePorts("project2", "wt2", 2)
	require.NoError(t, err)
	assert.Equal(t, []int{3102, 3103}, ports2)
}

func TestAllocatePorts_FindsGaps(t *testing.T) {
	cfg := NewConfig()

	// Allocate some ports
	_, err := cfg.AllocatePorts("project1", "wt1", 3)
	require.NoError(t, err)

	// Free the middle port
	cfg.FreePorts([]int{3101})

	// Allocate 1 port - should find the gap
	ports, err := cfg.AllocatePorts("project2", "wt2", 1)
	require.NoError(t, err)
	assert.Equal(t, []int{3101}, ports)
}

func TestAllocatePorts_RangeExhaustion(t *testing.T) {
	cfg := NewConfig()
	cfg.Defaults.PortRangeStart = 9000
	cfg.Defaults.PortRangeEnd = 9002 // Only 3 ports available

	// Allocate 2 ports
	_, err := cfg.AllocatePorts("p1", "w1", 2)
	require.NoError(t, err)

	// Try to allocate 2 more - should fail
	_, err = cfg.AllocatePorts("p2", "w2", 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no 2 consecutive ports")
}

func TestAllocatePorts_InvalidCount(t *testing.T) {
	cfg := NewConfig()

	_, err := cfg.AllocatePorts("project", "wt", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "port count must be positive")

	_, err = cfg.AllocatePorts("project", "wt", -1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "port count must be positive")
}

func TestAllocatePorts_SinglePort(t *testing.T) {
	cfg := NewConfig()

	ports, err := cfg.AllocatePorts("project", "wt", 1)

	require.NoError(t, err)
	assert.Equal(t, []int{3100}, ports)
	assert.Equal(t, 1, len(cfg.PortAllocations))
}

func TestFreePorts(t *testing.T) {
	cfg := NewConfig()

	// Allocate some ports
	ports, err := cfg.AllocatePorts("project", "wt", 3)
	require.NoError(t, err)
	assert.Equal(t, 3, len(cfg.PortAllocations))

	// Free all ports
	cfg.FreePorts(ports)
	assert.Equal(t, 0, len(cfg.PortAllocations))
}

func TestFreeWorktreePorts(t *testing.T) {
	cfg := NewConfig()

	// Allocate ports for different worktrees
	_, err := cfg.AllocatePorts("project", "wt1", 2)
	require.NoError(t, err)
	_, err = cfg.AllocatePorts("project", "wt2", 2)
	require.NoError(t, err)

	assert.Equal(t, 4, len(cfg.PortAllocations))

	// Free only wt1's ports
	cfg.FreeWorktreePorts("project", "wt1")

	assert.Equal(t, 2, len(cfg.PortAllocations))
	// Verify wt2's ports still exist
	assert.NotNil(t, cfg.PortAllocations["3102"])
	assert.NotNil(t, cfg.PortAllocations["3103"])
}

func TestGetProjectPorts(t *testing.T) {
	cfg := NewConfig()

	// Allocate ports for different projects
	_, err := cfg.AllocatePorts("project1", "wt1", 2)
	require.NoError(t, err)
	_, err = cfg.AllocatePorts("project2", "wt1", 3)
	require.NoError(t, err)

	// Get project1's ports
	ports := cfg.GetProjectPorts("project1")
	assert.Equal(t, []int{3100, 3101}, ports)

	// Get project2's ports
	ports = cfg.GetProjectPorts("project2")
	assert.Equal(t, []int{3102, 3103, 3104}, ports)

	// Non-existent project
	ports = cfg.GetProjectPorts("nonexistent")
	assert.Empty(t, ports)
}

func TestGetWorktreePorts(t *testing.T) {
	cfg := NewConfig()

	// Allocate ports for different worktrees
	_, err := cfg.AllocatePorts("project", "wt1", 2)
	require.NoError(t, err)
	_, err = cfg.AllocatePorts("project", "wt2", 3)
	require.NoError(t, err)

	// Get wt1's ports
	ports := cfg.GetWorktreePorts("project", "wt1")
	assert.Equal(t, []int{3100, 3101}, ports)

	// Get wt2's ports
	ports = cfg.GetWorktreePorts("project", "wt2")
	assert.Equal(t, []int{3102, 3103, 3104}, ports)
}

func TestIsPortAvailable(t *testing.T) {
	cfg := NewConfig()

	// Before allocation, port should be available
	assert.True(t, cfg.IsPortAvailable(3100))

	// Allocate the port
	_, err := cfg.AllocatePorts("project", "wt", 1)
	require.NoError(t, err)

	// Now it should not be available
	assert.False(t, cfg.IsPortAvailable(3100))

	// Port outside range should not be available
	assert.False(t, cfg.IsPortAvailable(2000))
	assert.False(t, cfg.IsPortAvailable(5000))
}

func TestTotalUsedPorts(t *testing.T) {
	cfg := NewConfig()

	assert.Equal(t, 0, cfg.TotalUsedPorts())

	_, err := cfg.AllocatePorts("p1", "w1", 3)
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.TotalUsedPorts())

	_, err = cfg.AllocatePorts("p2", "w1", 2)
	require.NoError(t, err)
	assert.Equal(t, 5, cfg.TotalUsedPorts())

	cfg.FreePorts([]int{3100, 3101})
	assert.Equal(t, 3, cfg.TotalUsedPorts())
}

func TestFindConsecutivePorts_EdgeCases(t *testing.T) {
	cfg := NewConfig()
	cfg.Defaults.PortRangeStart = 100
	cfg.Defaults.PortRangeEnd = 110

	// Empty allocation - should get first ports
	usedPorts := cfg.getUsedPorts()
	ports := cfg.findConsecutivePorts(usedPorts, 3)
	assert.Equal(t, []int{100, 101, 102}, ports)

	// Reset and allocate all ports except a gap at 105-106
	cfg.PortAllocations = make(map[string]*PortAlloc)
	for i := 100; i <= 110; i++ {
		if i != 105 && i != 106 {
			cfg.PortAllocations[strconv.Itoa(i)] = &PortAlloc{Project: "p", Worktree: "w", Index: 0}
		}
	}

	// Should find the gap of 2
	usedPorts = cfg.getUsedPorts()
	ports = cfg.findConsecutivePorts(usedPorts, 2)
	assert.Equal(t, []int{105, 106}, ports)

	// Should not find gap of 3
	ports = cfg.findConsecutivePorts(usedPorts, 3)
	assert.Nil(t, ports)
}

func TestGetAllPortInfo(t *testing.T) {
	cfg := NewConfig()

	// Allocate some ports
	_, err := cfg.AllocatePorts("project1", "wt1", 2)
	require.NoError(t, err)

	info := cfg.GetAllPortInfo()
	assert.Equal(t, 2, len(info))

	// Verify info is sorted by port
	assert.Equal(t, 3100, info[0].Port)
	assert.Equal(t, "project1", info[0].Project)
	assert.Equal(t, "wt1", info[0].Worktree)
	assert.Equal(t, 0, info[0].Index)

	assert.Equal(t, 3101, info[1].Port)
	assert.Equal(t, 1, info[1].Index)
}
