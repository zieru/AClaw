package telegram

import (
	"reflect"
	"testing"
)

func TestExtractInteractiveOptions(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectedTxt string
		expectedOpt []string
	}{
		{
			name:        "No options",
			input:       "Ini adalah jawaban biasa tanpa opsi.",
			expectedTxt: "Ini adalah jawaban biasa tanpa opsi.",
			expectedOpt: nil,
		},
		{
			name: "Standard pipe format",
			input: `Berikut adalah ringkasannya.
Apakah ada yang ingin ditindaklanjuti?

[OPSI: 📊 Tampilkan Grafik | 📑 Ekspor Laporan | 🔍 Analisis Lanjutan]`,
			expectedTxt: "Berikut adalah ringkasannya.\nApakah ada yang ingin ditindaklanjuti?",
			expectedOpt: []string{"📊 Tampilkan Grafik", "📑 Ekspor Laporan", "🔍 Analisis Lanjutan"},
		},
		{
			name: "Numbered format with OPTIONS tag",
			input: `Pilih salah satu langkah:
[OPTIONS:
1. Opsi Pertama
2. Opsi Kedua
]`,
			expectedTxt: "Pilih salah satu langkah:",
			expectedOpt: []string{"Opsi Pertama", "Opsi Kedua"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanText, opts := extractInteractiveOptions(tt.input)
			if cleanText != tt.expectedTxt {
				t.Errorf("cleanText = %q, want %q", cleanText, tt.expectedTxt)
			}
			if !reflect.DeepEqual(opts, tt.expectedOpt) {
				t.Errorf("opts = %v, want %v", opts, tt.expectedOpt)
			}
		})
	}
}
