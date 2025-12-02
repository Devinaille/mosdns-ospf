package ospf

import (
	"container/heap"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

// IpEntry represents an IP address along with its expiration time.
type IpEntry struct {
	ip             net.IPNet
	expirationTime time.Time
	domain         string
	queueIndex     int // The index of the entry in the priority queue
}

// PriorityQueue is a heap-based priority queue that orders IPs by their expiration time.
type PriorityQueue []*IpEntry

func (pq PriorityQueue) Len() int { return len(pq) }
func (pq PriorityQueue) Less(i, j int) bool {
	return pq[i].expirationTime.Before(pq[j].expirationTime)
}
func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].queueIndex = i
	pq[j].queueIndex = j
}

func (pq *PriorityQueue) Push(x interface{}) {
	ip := x.(*IpEntry)
	*pq = append(*pq, ip)
	ip.queueIndex = len(*pq) - 1
}

func (pq *PriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	*pq = old[0 : n-1]
	item.queueIndex = -1 // Reset the queue index when item is removed
	return item
}

// IpPool manages a pool of IP addresses and their expiration times.
type IpPool struct {
	ttl    time.Duration
	pool   map[string]map[string]*IpEntry // domain -> ip -> IpEntry
	pq     PriorityQueue                  // Priority queue for IPs
	router interface {
		AnnounceASBRRoute([]net.IPNet)
		RevokeASBRRoute([]net.IPNet)
	}
	logger *zap.Logger
	mux    sync.Mutex
	// persistence
	saveFile     string
	saveInterval time.Duration
}

// NewIpPool creates a new IpPool with the given TTL and router.
// NewIpPool creates a new IpPool with the given TTL and router.
func NewIpPool(ttl uint, router interface {
	AnnounceASBRRoute([]net.IPNet)
	RevokeASBRRoute([]net.IPNet)
}, logger *zap.Logger, saveFile string, saveIntervalSec uint) *IpPool {
	return &IpPool{
		ttl:          time.Duration(ttl) * time.Second,
		pool:         make(map[string]map[string]*IpEntry),
		pq:           make(PriorityQueue, 0),
		router:       router,
		logger:       logger,
		saveFile:     saveFile,
		saveInterval: time.Duration(saveIntervalSec) * time.Second,
	}
}

// AddIps adds or updates IPs for a given domain in the pool.
func (r *IpPool) AddIps(domain string, ips []net.IPNet) {
	r.mux.Lock()
	defer r.mux.Unlock()

	// Initialize the domain entry if it doesn't exist
	if _, exists := r.pool[domain]; !exists {
		r.pool[domain] = make(map[string]*IpEntry)
	}

	for _, ip := range ips {
		ipStr := ip.String()
		expirationTime := time.Now().Add(r.ttl)

		// If the IP exists, update its expiration time
		if entry, exists := r.pool[domain][ipStr]; exists {
			entry.expirationTime = expirationTime
			heap.Fix(&r.pq, entry.queueIndex) // Update the position in the heap
			r.logger.Info("Updated IP expiration", zap.String("domain", domain), zap.String("ip", ipStr), zap.Time("expiration", expirationTime))
		} else {
			// Otherwise, add the new IP to the pool and priority queue
			newEntry := &IpEntry{
				ip:             ip,
				expirationTime: expirationTime,
				domain:         domain,
			}
			r.pool[domain][ipStr] = newEntry
			heap.Push(&r.pq, newEntry)
			r.logger.Info("Added new IP to pool", zap.String("domain", domain), zap.String("ip", ipStr), zap.Time("expiration", expirationTime))
		}
	}
}

// startExpireChecker starts a background goroutine that checks for expired IPs every second.
func (r *IpPool) startExpireChecker() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		r.cleanExpiredEntries()
	}
}

// startAutoSave starts a background goroutine that periodically saves the pool to r.saveFile.
func (r *IpPool) startAutoSave() {
	if r.saveFile == "" || r.saveInterval <= 0 {
		return
	}
	ticker := time.NewTicker(r.saveInterval)
	defer ticker.Stop()
	for range ticker.C {
		if err := r.saveToFile(r.saveFile); err != nil {
			r.logger.Error("failed to autosave ip pool", zap.String("file", r.saveFile), zap.Error(err))
		} else {
			r.logger.Debug("autosaved ip pool", zap.String("file", r.saveFile))
		}
	}
}

// SaveToFile saves the current pool to the provided path.
func (r *IpPool) SaveToFile(path string) error {
	return r.saveToFile(path)
}

// LoadFromFile loads pool entries from the provided path. Expired entries are ignored.
func (r *IpPool) LoadFromFile(path string) error {
	return r.loadFromFile(path)
}

type persistEntry struct {
	Domain     string `json:"domain"`
	IP         string `json:"ip"`
	Expiration int64  `json:"expiration"` // unix nano
}

func (r *IpPool) saveToFile(path string) error {
	r.mux.Lock()
	defer r.mux.Unlock()

	var entries []persistEntry
	for domain, m := range r.pool {
		for ipStr, e := range m {
			entries = append(entries, persistEntry{
				Domain:     domain,
				IP:         ipStr,
				Expiration: e.expirationTime.UnixNano(),
			})
		}
	}

	if len(entries) == 0 {
		// remove file if exists
		_ = os.Remove(path)
		return nil
	}

	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}

	// ensure dir exists
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (r *IpPool) loadFromFile(path string) error {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		// not fatal if file doesn't exist
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var entries []persistEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return err
	}

	now := time.Now()

	r.mux.Lock()
	defer r.mux.Unlock()

	// reset current structures
	r.pool = make(map[string]map[string]*IpEntry)
	r.pq = make(PriorityQueue, 0)

	for _, pe := range entries {
		if pe.Expiration <= 0 {
			continue
		}
		exp := time.Unix(0, pe.Expiration)
		if exp.Before(now) {
			continue
		}
		// parse ip string
		// net.ParseCIDR returns ip and *net.IPNet
		_, ipnet, err := net.ParseCIDR(pe.IP)
		if err != nil {
			// try parse as plain ip -> append /32
			parsed := net.ParseIP(pe.IP)
			if parsed == nil {
				r.logger.Warn("invalid ip entry in persistence file", zap.String("ip", pe.IP))
				continue
			}
			ipnet = &net.IPNet{IP: parsed, Mask: net.CIDRMask(32, 32)}
		}

		domain := pe.Domain
		ipStr := ipnet.String()
		if _, ok := r.pool[domain]; !ok {
			r.pool[domain] = make(map[string]*IpEntry)
		}
		e := &IpEntry{
			ip:             *ipnet,
			expirationTime: exp,
			domain:         domain,
		}
		r.pool[domain][ipStr] = e
		heap.Push(&r.pq, e)
	}

	// after loading, announce to router the still-valid IPs
	var toAnnounce []net.IPNet
	for _, m := range r.pool {
		for _, e := range m {
			toAnnounce = append(toAnnounce, e.ip)
		}
	}
	if len(toAnnounce) > 0 {
		r.router.AnnounceASBRRoute(toAnnounce)
		r.logger.Info("restored ip pool and announced routes", zap.Int("count", len(toAnnounce)), zap.String("file", path))
	}

	return nil
}

// cleanExpiredEntries removes expired IPs from the pool and revokes them from the router.
func (r *IpPool) cleanExpiredEntries() {
	r.mux.Lock()
	defer r.mux.Unlock()

	now := time.Now()
	var revokedIps []net.IPNet

	// Check the top of the priority queue for expired IPs
	for len(r.pq) > 0 && r.pq[0].expirationTime.Before(now) {
		// Remove the expired IP from the heap
		expiredIp := heap.Pop(&r.pq).(*IpEntry)
		delete(r.pool[expiredIp.domain], expiredIp.ip.String()) // Remove from the pool
		revokedIps = append(revokedIps, expiredIp.ip)
		r.logger.Info("Removed expired IP", zap.String("domain", expiredIp.domain), zap.String("ip", expiredIp.ip.String()), zap.Time("expiration", expiredIp.expirationTime))
	}

	// If there are revoked IPs, notify the router
	if len(revokedIps) > 0 {
		r.router.RevokeASBRRoute(revokedIps)
		r.logger.Info("Revoked expired IPs", zap.Int("count", len(revokedIps)))
	}
}

// Init Initialize the IpPool and start the background expiration checker.
func (r *IpPool) Init() {
	r.logger.Info("IpPool initialized", zap.Duration("ttl", r.ttl))
	// load persisted file if configured
	if r.saveFile != "" {
		if err := r.loadFromFile(r.saveFile); err != nil {
			r.logger.Error("failed to load ip pool file", zap.String("file", r.saveFile), zap.Error(err))
		}
	}

	// start background tasks
	go r.startExpireChecker()
	if r.saveFile != "" && r.saveInterval > 0 {
		go r.startAutoSave()
	}
}
