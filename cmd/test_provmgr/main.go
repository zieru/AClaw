package main

import (
	"context"
	"fmt"
	"log"

	"goassistant/internal/provider"
	"goassistant/internal/storage"
)

func main() {
	db, err := storage.Open("data/goassistant.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	provMgr := provider.GetManager()
	dbProviders, _ := db.ListProviders()
	for _, p := range dbProviders {
		if !p.IsActive {
			continue
		}
		inst := provider.NewOpenAIProviderWithKeys(p.Name, p.Type, p.BaseURL, p.APIKeys, p.KeyStrategy, p.DefaultModel, p.Models)
		provMgr.RegisterWithID(p.ID, inst, p.Priority)
		fmt.Printf("Registered ID='%s' Name='%s'\n", p.ID, p.Name)
	}

	p1, ok1 := provMgr.Get("hcnsec.cn")
	fmt.Printf("Get('hcnsec.cn'): ok=%v, p=%v\n", ok1, p1 != nil)
	p2, ok2 := provMgr.Get("Hcnsec.Cn (Custom)")
	fmt.Printf("Get('Hcnsec.Cn (Custom)'): ok=%v, p=%v\n", ok2, p2 != nil)
	p3, ok3 := provMgr.Get("hcnsec")
	fmt.Printf("Get('hcnsec'): ok=%v, p=%v\n", ok3, p3 != nil)

	// Now test GenerateWithFallback with preferredName = "hcnsec.cn" and req.Model = "auto"
	chatReq := provider.ChatRequest{
		Model: "auto",
		Messages: []provider.ChatMessage{
			{Role: provider.RoleUser, Content: "halo"},
		},
	}
	resp, err := provMgr.GenerateWithFallback(context.Background(), "hcnsec.cn", chatReq)
	if err != nil {
		fmt.Printf("GenerateWithFallback error: %v\n", err)
	} else {
		fmt.Printf("GenerateWithFallback SUCCESS! Resp ProviderName='%s', Model='%s'\n", resp.ProviderName, resp.Model)
	}
}
