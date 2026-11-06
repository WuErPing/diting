package audio

func S16ToFloat32(input []byte) []float32 {
	samples := make([]float32, len(input)/2)
	for i := range samples {
		value := int16(input[2*i]) | int16(input[2*i+1])<<8
		samples[i] = float32(value) / 32768
	}
	return samples
}
