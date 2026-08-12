package l4lbdrv

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/netip"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/dchest/siphash"
	"github.com/vishvananda/netlink"
	"go.uber.org/multierr"
)

type Config struct {
	BinPath        string
	InterfaceName  string
	XdpCapHookPath string

	VIP   netip.Addr
	Dests DestinationEntries // ルックアップテーブルのサイズ

	HealthCheckEndpoint string
}

type L4LB struct {
	cfg *Config

	bindings     *Bindings
	linkAttacher *LinkAttacher

	deadDests []bool // true -> dead, false -> alive

	// Maglev
	offsets []uint32 // ルックアップテーブルを生成するためのoffset (len N)
	skips   []uint32 // ルックアップテーブルを生成するためのskips (len N)
}

func New(cfg *Config) (*L4LB, error) {
	if len(cfg.Dests) < 2 {
		return nil, fmt.Errorf("Dests must have at least 2 entries (Dests[0] is the l4lb itself), but got %d", len(cfg.Dests))
	}

	if err := PrepSystemForXDP(); err != nil {
		return nil, fmt.Errorf("Failed to prep system for XDP: %w", err)
	}
	aBinPath, err := filepath.Abs(cfg.BinPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to get absolute path for %s: %w", cfg.BinPath, err)
	}
	var aXdpcapHookPath string
	if cfg.XdpCapHookPath != "" {
		aXdpcapHookPath, err = filepath.Abs(cfg.XdpCapHookPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to get absolute path for %s: %w", cfg.XdpCapHookPath, err)
		}
	}
	bindings, err := BindBalancer(aBinPath, aXdpcapHookPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to bind balancer: %w", err)
	}

	lb := &L4LB{
		cfg:      cfg,
		bindings: bindings,
	}

	lb.deadDests = make([]bool, len(lb.cfg.Dests))
	for i := range lb.deadDests {
		lb.deadDests[i] = false
	}

	err = lb.PopulateLookupTable()
	if err != nil {
		return nil, fmt.Errorf("Failed to populate lookup-table: %w", err)
	}
	slog.Info("PopulateLookupTable finished")

	var link netlink.Link
	if cfg.InterfaceName == "" {
		slog.Info("No interface name provided, skipping link attachment.")
	} else {
		l, err := netlink.LinkByName(cfg.InterfaceName)
		if err != nil {
			return nil, fmt.Errorf("Failed to find interface %q: %w", cfg.InterfaceName, err)
		}
		link = l
	}
	if link != nil {
		a, err := AttachToLink(link, bindings.LBMain.FD())
		if err != nil {
			return nil, multierr.Combine(err, bindings.Close())
		}
		lb.linkAttacher = a
	}
	if err := lb.Sync(); err != nil {
		return nil, fmt.Errorf("Initial map sync failed: %w", err)
	}

	return lb, nil
}

func (lb *L4LB) generateOffsetsAndSkips() {
	lb.offsets = make([]uint32, len(lb.cfg.Dests))
	lb.skips = make([]uint32, len(lb.cfg.Dests))

	for i, dest := range lb.cfg.Dests {
		h := siphash.Hash(0xdeadbeef, 0, []byte(dest.IPAddr.String()))
		lb.offsets[i] = uint32(h>>32) % MAGLEV_TABLE_SIZE
		lb.skips[i] = uint32(h&0xffffffff)%(MAGLEV_TABLE_SIZE-1) + 1
	}
}

var hostOrder = binary.LittleEndian

// ルックアップテーブルを計算して生成する関数
func (lb *L4LB) PopulateLookupTable() error {
	aliveDestsCount := 0
	for i := 1; i < len(lb.deadDests); i++ {
		if !lb.deadDests[i] {
			aliveDestsCount += 1
		}
	}
	if aliveDestsCount == 0 {
		slog.Warn("All destinations are dead. Keeping the current lookup table.")
		return nil
	}

	// LookupTable生成に必要なoffsetsとskipsを計算
	lb.generateOffsetsAndSkips()

	table := make([]int, MAGLEV_TABLE_SIZE)
	tableIndices := make([]uint32, MAGLEV_TABLE_SIZE)
	for i := range tableIndices {
		tableIndices[i] = uint32(i)
	}
	for j := range table {
		table[j] = -1
	}

	var n uint32 = 0
	for {
		// for each backends
		for i := 1; i < len(lb.cfg.Dests); i++ {
			if lb.deadDests[i] {
				continue
			}

			c := lb.nextOffset(i)
			for table[c] >= 0 {
				c = lb.nextOffset(i)
			}

			table[c] = i
			n++
			if n == MAGLEV_TABLE_SIZE {
				// intをuint32に詰め替え
				values := make([]uint32, len(table))
				for i := range table {
					values[i] = uint32(table[i])
				}
				for i, v := range values {
					if err := lb.bindings.MaglevLookupTable.Put(uint32(i), v); err != nil {
						return fmt.Errorf("maglev put %d: %w", i, err)
					}
				}
				return nil
			}
		}
	}
}

// 次のオフセットを計算する
func (lb *L4LB) nextOffset(i int) uint32 {
	res := lb.offsets[i]

	lb.offsets[i] += lb.skips[i]
	if lb.offsets[i] >= MAGLEV_TABLE_SIZE {
		lb.offsets[i] -= MAGLEV_TABLE_SIZE
	}
	return res
}

func (lb *L4LB) HealthCheck(dest DestinationEntry) bool {
	url := "http://" + dest.IPAddr.String() + lb.cfg.HealthCheckEndpoint
	c := &http.Client{
		Timeout: 500 * time.Millisecond,
	}
	// slog.Info("healthchecking at ", slog.String("url", url))
	resp, err := c.Get(url)
	if err != nil {
		// TODO: Logging
		return false
	}

	isSuccess := resp.StatusCode == http.StatusOK
	if !isSuccess {
		// TODO: Logging
		return false
	}
	return true
}

func (lb *L4LB) HealthCheckAll() bool {
	wg := &sync.WaitGroup{}
	var changed []int

	// slog.Info("Do health check...")

	wg.Add(len(lb.cfg.Dests) - 1)
	for i, dest := range lb.cfg.Dests {
		if i == 0 { // Dests[0] = l4lb itself
			continue
		}
		go func() {
			defer wg.Done()
			ok := lb.HealthCheck(dest)
			if !ok && !lb.deadDests[i] { // 新しく倒れた場合
				lb.deadDests[i] = true
				slog.Info("Become dead: ", slog.Int("index", i))
				changed = append(changed, i)
			} else if ok && lb.deadDests[i] { // 生き返った場合
				lb.deadDests[i] = false
				slog.Info("Become alive: ", slog.Int("index", i))
				changed = append(changed, i)
			}
		}()
	}
	wg.Wait()
	if len(changed) > 0 {
		return true
	} else {
		return false
	}
}

func IPToUint32(ip netip.Addr) (uint32, error) {
	if !ip.Is4() {
		return 0, errors.New("Given IP is not an IPv4 address.")
	}

	ip4 := ip.As4()
	return hostOrder.Uint32(ip4[:]), nil
}

func (lb *L4LB) Sync() error {
	vip4, err := IPToUint32(lb.cfg.VIP)
	if err != nil {
		return fmt.Errorf("vip: %w", err)
	}

	err = lb.bindings.ConfigMap.Update(uint32(0), &LbConfig{
		VipAddress: vip4,
		NumDests:   uint32(len(lb.cfg.Dests) - 1),
	}, 0)
	if err != nil {
		return fmt.Errorf("Failed to update ConfigMap: %w", err)
	}

	keys := make([]uint32, len(lb.cfg.Dests))
	for i := range keys {
		keys[i] = uint32(i)
	}

	_, err = lb.bindings.DestinationArray.BatchUpdate(keys, lb.cfg.Dests, &ebpf.BatchOptions{})
	if err != nil {
		return fmt.Errorf("Failed to update DestinationArray: %w", err)
	}

	return nil
}

func (lb *L4LB) Close() error {
	err := lb.linkAttacher.Close()
	if err != nil {
		return err
	}
	return lb.bindings.Close()
}

func (lb *L4LB) DumpCounters() error {
	cnt, err := lb.bindings.ReadStatCountersAggregate()
	if err != nil {
		return err
	}

	slog.Info(cnt.String())

	return nil
}

// `PrepSystemForXDP` configures RLIMIT_MEMLOCK to ensure enough room to
// allocate eBPF programs and maps on older Linux systems.
func PrepSystemForXDP() error {
	const RLIMIT_MEMLOCK = 8
	var rlim syscall.Rlimit
	if err := syscall.Getrlimit(RLIMIT_MEMLOCK, &rlim); err != nil {
		return fmt.Errorf("Failed to Getrlimit(RLIMIT_MEMLOCK): %v", err)
	}
	slog.Info("Getrlimit(RLIMIT_MEMLOCK)", "Cur", rlim.Cur, "Max", rlim.Max)

	rlim.Cur = math.MaxUint64
	rlim.Max = math.MaxUint64
	if err := syscall.Setrlimit(RLIMIT_MEMLOCK, &rlim); err != nil {
		return fmt.Errorf("Failed to Setrlimit(RLIMIT_MEMLOCK): %v", err)
	}
	slog.Info("Setrlimit(RLIMIT_MEMLOCK)", "Cur", rlim.Cur, "Max", rlim.Max)

	return nil
}
