package audio

import (
	"fmt"
	"log"
	"sync/atomic"

	"github.com/gen2brain/malgo"
)

type Capture struct {
	context *malgo.AllocatedContext
	device  *malgo.Device
}

func StartCapture(output chan<- []float32, dropped *atomic.Uint64) (*Capture, error) {
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(message string) {
		log.Printf("音频设备: %s", message)
	})
	if err != nil {
		return nil, fmt.Errorf("initialize audio context: %w", err)
	}

	config := malgo.DefaultDeviceConfig(malgo.Capture)
	config.Capture.Format = malgo.FormatS16
	config.Capture.Channels = 1
	config.SampleRate = 16000
	config.PeriodSizeInMilliseconds = 20

	callbacks := malgo.DeviceCallbacks{
		Data: func(_, input []byte, _ uint32) {
			if len(input) == 0 {
				return
			}
			samples := S16ToFloat32(input)
			select {
			case output <- samples:
			default:
				dropped.Add(1)
			}
		},
	}
	device, err := malgo.InitDevice(context.Context, config, callbacks)
	if err != nil {
		_ = context.Uninit()
		context.Free()
		return nil, fmt.Errorf("open microphone: %w", err)
	}
	if err := device.Start(); err != nil {
		device.Uninit()
		_ = context.Uninit()
		context.Free()
		return nil, fmt.Errorf("start microphone: %w", err)
	}
	return &Capture{context: context, device: device}, nil
}

func (c *Capture) Close() {
	if c == nil {
		return
	}
	if c.device != nil {
		c.device.Uninit()
	}
	if c.context != nil {
		_ = c.context.Uninit()
		c.context.Free()
	}
}
