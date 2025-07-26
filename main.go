package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
	bolt "go.etcd.io/bbolt"
	"github.com/spf13/cobra"
)

// Config represents a roll configuration
type Config struct {
	Name     string `toml:"name"`
	Chance   int    `toml:"chance"`
	Grace    int    `toml:"grace"`
	Pity     int    `toml:"pity"`
	Variance int    `toml:"variance"`
}

// State represents the current state for a config
type State struct {
	PityCounter int `json:"pity_counter"`
	LastRoll    int `json:"last_roll"`
}

var (
	db         *bolt.DB
	configDir  string
	dbPath     string
	rootCmd    = &cobra.Command{
		Use:   "roll",
		Short: "A probability-based roll system with pity mechanics",
	}
)

func init() {
	// Set up config directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	configDir = filepath.Join(homeDir, ".roll")
	dbPath = filepath.Join(configDir, "roll.db")

	// Create config directory if it doesn't exist
	if err := os.MkdirAll(configDir, 0755); err != nil {
		log.Fatal(err)
	}

	// Initialize random seed
	rand.Seed(time.Now().UnixNano())

	// Add commands
	rootCmd.AddCommand(createCmd)
	rootCmd.AddCommand(rollCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(showCmd)
	rootCmd.AddCommand(deleteCmd)
	rootCmd.AddCommand(diceCmd)
}

var createCmd = &cobra.Command{
	Use:   "create [name] [chance] [grace] [pity] [variance]",
	Short: "Create a new roll configuration",
	Args:  cobra.ExactArgs(5),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		chance, err := strconv.Atoi(args[1])
		if err != nil {
			log.Fatal("Invalid chance value:", err)
		}
		grace, err := strconv.Atoi(args[2])
		if err != nil {
			log.Fatal("Invalid grace value:", err)
		}
		pity, err := strconv.Atoi(args[3])
		if err != nil {
			log.Fatal("Invalid pity value:", err)
		}
		variance, err := strconv.Atoi(args[4])
		if err != nil {
			log.Fatal("Invalid variance value:", err)
		}

		// Validate values
		if chance < 0 || chance > 100 {
			log.Fatal("Chance must be between 0 and 100")
		}
		if grace < 0 {
			log.Fatal("Grace must be non-negative")
		}
		if pity < 0 {
			log.Fatal("Pity must be non-negative")
		}
		if variance < 0 {
			log.Fatal("Variance must be non-negative")
		}

		config := Config{
			Name:     name,
			Chance:   chance,
			Grace:    grace,
			Pity:     pity,
			Variance: variance,
		}

		// Save config to TOML file
		configPath := filepath.Join(configDir, name+".toml")
		file, err := os.Create(configPath)
		if err != nil {
			log.Fatal("Failed to create config file:", err)
		}
		defer file.Close()

		if err := toml.NewEncoder(file).Encode(config); err != nil {
			log.Fatal("Failed to write config:", err)
		}

		// Initialize state in database
		err = db.Update(func(tx *bolt.Tx) error {
			b, err := tx.CreateBucketIfNotExists([]byte("states"))
			if err != nil {
				return err
			}

			state := State{PityCounter: 0, LastRoll: 0}
			data, err := json.Marshal(state)
			if err != nil {
				return err
			}

			return b.Put([]byte(name), data)
		})

		if err != nil {
			log.Fatal("Failed to initialize state:", err)
		}

		fmt.Printf("Created roll configuration '%s' with:\n", name)
		fmt.Printf("  Chance: %d%%\n", chance)
		fmt.Printf("  Grace: %d%%\n", grace)
		fmt.Printf("  Pity: %d rolls\n", pity)
		fmt.Printf("  Variance: 1-%d chance of adding grace (%d%%)\n", variance, grace)
		fmt.Printf("\nConfig saved to: %s\n", configPath)
	},
}

var rollCmd = &cobra.Command{
	Use:   "roll [name]",
	Short: "Roll using a configuration",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		// Load config
		config, err := loadConfig(name)
		if err != nil {
			log.Fatal("Failed to load config:", err)
		}

		// Load state
		var state State
		err = db.Update(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte("states"))
			if b == nil {
				return fmt.Errorf("states bucket not found")
			}

			data := b.Get([]byte(name))
			if data == nil {
				return fmt.Errorf("state not found for %s", name)
			}

			if err := json.Unmarshal(data, &state); err != nil {
				return err
			}

			// Calculate effective chance
			effectiveChance := config.Chance + (state.PityCounter * config.Grace)
			
			// Apply variance - adds grace value with 1/variance chance
			if config.Variance > 0 {
				varianceRoll := rand.Intn(config.Variance) + 1
				if rand.Intn(varianceRoll) == 0 {
					effectiveChance += config.Grace
				}
			}

			// Cap at 100%
			if effectiveChance > 100 {
				effectiveChance = 100
			}

			// Roll
			roll := rand.Intn(100) + 1
			success := roll <= effectiveChance

			fmt.Printf("\n🎲 Rolling '%s'...\n", name)
			fmt.Printf("Base chance: %d%%\n", config.Chance)
			fmt.Printf("Pity counter: %d\n", state.PityCounter)
			fmt.Printf("Grace bonus: %d%%\n", state.PityCounter*config.Grace)
			fmt.Printf("Effective chance: %d%%\n", effectiveChance)
			fmt.Printf("Roll: %d\n", roll)

			if success {
				fmt.Printf("\n✅ SUCCESS! 🎉\n")
				state.PityCounter = 0
			} else {
				fmt.Printf("\n❌ FAILED\n")
				if state.PityCounter < config.Pity {
					state.PityCounter++
				}
			}

			state.LastRoll = roll

			// Save updated state
			data, err = json.Marshal(state)
			if err != nil {
				return err
			}

			return b.Put([]byte(name), data)
		})

		if err != nil {
			log.Fatal("Failed to update state:", err)
		}
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all roll configurations",
	Run: func(cmd *cobra.Command, args []string) {
		files, err := os.ReadDir(configDir)
		if err != nil {
			log.Fatal("Failed to read config directory:", err)
		}

		fmt.Println("Available configurations:")
		for _, file := range files {
			if filepath.Ext(file.Name()) == ".toml" {
				name := file.Name()[:len(file.Name())-5]
				
				// Load config to show details
				config, err := loadConfig(name)
				if err != nil {
					continue
				}

				// Get state
				var state State
				db.View(func(tx *bolt.Tx) error {
					b := tx.Bucket([]byte("states"))
					if b != nil {
						data := b.Get([]byte(name))
						if data != nil {
							json.Unmarshal(data, &state)
						}
					}
					return nil
				})

				fmt.Printf("\n  %s:\n", name)
				fmt.Printf("    Chance: %d%% | Grace: %d%% | Pity: %d | Variance: 1-%d chance\n", 
					config.Chance, config.Grace, config.Pity, config.Variance)
				fmt.Printf("    Current pity: %d\n", state.PityCounter)
			}
		}
	},
}

var showCmd = &cobra.Command{
	Use:   "show [name]",
	Short: "Show details of a roll configuration",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		config, err := loadConfig(name)
		if err != nil {
			log.Fatal("Failed to load config:", err)
		}

		var state State
		err = db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte("states"))
			if b == nil {
				return fmt.Errorf("states bucket not found")
			}

			data := b.Get([]byte(name))
			if data == nil {
				return fmt.Errorf("state not found")
			}

			return json.Unmarshal(data, &state)
		})

		if err != nil {
			log.Fatal("Failed to load state:", err)
		}

		fmt.Printf("Configuration '%s':\n", name)
		fmt.Printf("  Base chance: %d%%\n", config.Chance)
		fmt.Printf("  Grace: %d%% per fail\n", config.Grace)
		fmt.Printf("  Max pity: %d rolls\n", config.Pity)
		fmt.Printf("  Variance: 1-%d chance of adding grace (%d%%)\n", config.Variance, config.Grace)
		fmt.Printf("\nCurrent state:\n")
		fmt.Printf("  Pity counter: %d\n", state.PityCounter)
		fmt.Printf("  Current chance: %d%%\n", config.Chance+(state.PityCounter*config.Grace))
		fmt.Printf("  Last roll: %d\n", state.LastRoll)
		fmt.Printf("\nConfig file: %s\n", filepath.Join(configDir, name+".toml"))
	},
}

var deleteCmd = &cobra.Command{
	Use:   "delete [name]",
	Short: "Delete a roll configuration",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		// Delete config file
		configPath := filepath.Join(configDir, name+".toml")
		if err := os.Remove(configPath); err != nil {
			log.Fatal("Failed to delete config file:", err)
		}

		// Delete state from database
		err := db.Update(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte("states"))
			if b != nil {
				return b.Delete([]byte(name))
			}
			return nil
		})

		if err != nil {
			log.Fatal("Failed to delete state:", err)
		}

		fmt.Printf("Deleted configuration '%s'\n", name)
	},
}

var diceCmd = &cobra.Command{
	Use:   "dice [type]",
	Short: "Roll dice (d4, d5, d6, d8, d10, d12, d20, d100)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		diceType := args[0]
		
		// Get shift value from flag
		shift, _ := cmd.Flags().GetInt("shift")
		
		var sides int
		
		// Parse dice type
		switch diceType {
		case "d10", "D10":
			sides = 10
		case "d5", "D5":
			sides = 5
		case "d4", "D4":
			sides = 4
		case "d6", "D6":
			sides = 6
		case "d8", "D8":
			sides = 8
		case "d12", "D12":
			sides = 12
		case "d20", "D20":
			sides = 20
		case "d100", "D100":
			sides = 100
		default:
			log.Fatal("Invalid dice type. Supported: d4, d5, d6, d8, d10, d12, d20, d100")
		}
		
		// Roll the dice
		roll := rand.Intn(sides) + 1
		
		fmt.Printf("\n🎲 Rolling %s...\n", diceType)
		fmt.Printf("Roll: %d\n", roll)
		
		if shift != 0 {
			result := roll + shift
			fmt.Printf("Shifted result: %d (roll + %d)\n", result, shift)
			fmt.Printf("\nRange for %s with shift: %d-%d\n", diceType, 1+shift, sides+shift)
		} else {
			fmt.Printf("\nStandard range for %s: 1-%d\n", diceType, sides)
		}
	},
}

func init() {
	// Add shift flag to dice command
	diceCmd.Flags().IntP("shift", "s", 0, "Shift the dice result by this amount")
}

func loadConfig(name string) (*Config, error) {
	configPath := filepath.Join(configDir, name+".toml")
	var config Config
	
	if _, err := toml.DecodeFile(configPath, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

func main() {
	// Open database
	var err error
	db, err = bolt.Open(dbPath, 0600, nil)
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer db.Close()

	// Execute command
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

