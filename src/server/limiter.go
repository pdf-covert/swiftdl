// 全局限速:所有任务的所有连接共享。允许透支的令牌桶——刚收到的字节先拿
// 走,欠下的账睡过去。热路径只有一次短暂持锁,任意并发连接都适用。

package main

import (
	"sync"
	"sync/atomic"
	"time"
)

type SpeedLimiter struct {
	// 字节/秒;0 = 不限。
	rate   atomic.Int64
	mu     sync.Mutex
	bucket float64
	at     time.Time
}

func NewSpeedLimiter(bytesPerSec int64) *SpeedLimiter {
	l := &SpeedLimiter{at: time.Now()}
	l.rate.Store(bytesPerSec)
	return l
}

func (l *SpeedLimiter) SetRate(bytesPerSec int64) { l.rate.Store(bytesPerSec) }

func (l *SpeedLimiter) Rate() int64 { return l.rate.Load() }

// 拿走 bytes 个令牌,不足部分睡掉。
func (l *SpeedLimiter) Take(bytes int) {
	rate := l.rate.Load()
	if rate == 0 {
		return
	}
	var wait time.Duration
	l.mu.Lock()
	now := time.Now()
	refill := now.Sub(l.at).Seconds() * float64(rate)
	// 最多允许一秒的突发。
	l.bucket = min(l.bucket+refill, float64(rate)) - float64(bytes)
	l.at = now
	if l.bucket < 0 {
		wait = time.Duration(-l.bucket / float64(rate) * float64(time.Second))
	}
	l.mu.Unlock()
	if wait > 0 {
		time.Sleep(wait)
	}
}
