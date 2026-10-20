package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server         ServerConfig         `mapstructure:"server"`
	Database       DatabaseConfig       `mapstructure:"database"`
	Security       SecurityConfig       `mapstructure:"security"`
	Logging        LoggingConfig        `mapstructure:"logging"`
	Query          QueryConfig          `mapstructure:"query"`
	MCP            MCPConfig            `mapstructure:"mcp"`
	SQLTranslation SQLTranslationConfig `mapstructure:"sql_translation"`
}

type ServerConfig struct {
	Port                string        `mapstructure:"port"`
	Mode                string        `mapstructure:"mode"`
	Host                string        `mapstructure:"host"`
	ReadHeaderTimeout   time.Duration `mapstructure:"read_header_timeout"`
	ReadTimeout         time.Duration `mapstructure:"read_timeout"`
	WriteTimeout        time.Duration `mapstructure:"write_timeout"`
	IdleTimeout         time.Duration `mapstructure:"idle_timeout"`
	MaxRequestBodyBytes int64         `mapstructure:"max_request_body_bytes"`
}

type DatabaseConfig struct {
	Host     string `mapstructure:"host"`
	Port     string `mapstructure:"port"`
	Database string `mapstructure:"database"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	SSL      string `mapstructure:"ssl"`
}

type SecurityConfig struct {
	JWTSecret          string        `mapstructure:"jwt_secret"`
	JWTExpiration      time.Duration `mapstructure:"jwt_expiration"`
	RateLimitPerMinute int           `mapstructure:"rate_limit_per_minute"`
	RateLimitBurst     int           `mapstructure:"rate_limit_burst"`
	EnableAuth         bool          `mapstructure:"enable_auth"`
	EnableRateLimit    bool          `mapstructure:"enable_rate_limit"`
	// EnforceReadOnly opens database sessions read-only where the engine
	// supports it (#20). It cannot be disabled in release mode.
	EnforceReadOnly bool `mapstructure:"enforce_read_only"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./configs")
	viper.AddConfigPath(".")

	// Set default values
	setDefaults()

	// Environment variables follow the config path: uppercase each path
	// segment and replace dots with underscores (server.read_timeout becomes
	// SERVER_READ_TIMEOUT). Keep the historical DATABASE_NAME and JWT_SECRET
	// spellings as aliases for their canonical keys.
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	_ = viper.BindEnv("database.database", "DATABASE_DATABASE", "DATABASE_NAME")
	_ = viper.BindEnv("database.username", "DATABASE_USERNAME")
	_ = viper.BindEnv("database.password", "DATABASE_PASSWORD")
	_ = viper.BindEnv("security.jwt_secret", "SECURITY_JWT_SECRET", "JWT_SECRET")

	// Read config file
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Config file not found, use defaults and environment
			fmt.Println("Config file not found, using defaults and environment variables")
		} else {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	return &config, nil
}

func setDefaults() {
	// Server defaults
	viper.SetDefault("server.port", "8080")
	viper.SetDefault("server.mode", "debug")
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.read_header_timeout", 10*time.Second)
	viper.SetDefault("server.read_timeout", 30*time.Second)
	// Fetch queries may run for up to 30 minutes; leave room for response
	// encoding and writing the final batch.
	viper.SetDefault("server.write_timeout", 31*time.Minute)
	viper.SetDefault("server.idle_timeout", 120*time.Second)
	viper.SetDefault("server.max_request_body_bytes", int64(1<<20))

	// Database defaults
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", "3306")
	viper.SetDefault("database.database", "gateway_db")
	viper.SetDefault("database.username", "gateway_user")
	viper.SetDefault("database.ssl", "false")

	// Security defaults
	// jwt_secret has no default on purpose: ValidateSecurity rejects an
	// empty or placeholder secret whenever authentication is enabled.
	viper.SetDefault("security.jwt_expiration", "24h")
	viper.SetDefault("security.rate_limit_per_minute", 60)
	viper.SetDefault("security.rate_limit_burst", 10)
	viper.SetDefault("security.enable_auth", true)
	viper.SetDefault("security.enable_rate_limit", true)
	viper.SetDefault("security.enforce_read_only", true)

	// Logging defaults
	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.format", "json")

	// Query defaults
	viper.SetDefault("query.prefer_streaming", true)
	viper.SetDefault("query.execution_mode", "auto") // auto, streaming, pagination

	// SQL translation runtime limits and process configuration.
	viper.SetDefault("sql_translation.enabled", true)
	viper.SetDefault("sql_translation.timeout", 5*time.Second)
	viper.SetDefault("sql_translation.max_concurrent", 4)
	viper.SetDefault("sql_translation.max_sql_bytes", int64(1<<20))
	viper.SetDefault("sql_translation.max_output_bytes", int64(1<<20))
	viper.SetDefault("sql_translation.python_path", "python3")
	viper.SetDefault("sql_translation.script_path", "./scripts/sql_translate.py")
	viper.SetDefault("sql_translation.python_module_path", "/opt/data-fabric/python")
	// MCP defaults to local stdio. Remote Streamable HTTP listens on loopback unless direct TLS is configured.
	viper.SetDefault("mcp.enabled", false)
	viper.SetDefault("mcp.transport", "stdio")
	viper.SetDefault("mcp.address", "127.0.0.1:8081")
	viper.SetDefault("mcp.tls_cert_file", "")
	viper.SetDefault("mcp.tls_key_file", "")
}

type SQLTranslationConfig struct {
	Enabled          bool          `mapstructure:"enabled"`
	Timeout          time.Duration `mapstructure:"timeout"`
	MaxConcurrent    int           `mapstructure:"max_concurrent"`
	MaxSQLBytes      int64         `mapstructure:"max_sql_bytes"`
	MaxOutputBytes   int64         `mapstructure:"max_output_bytes"`
	PythonPath       string        `mapstructure:"python_path"`
	ScriptPath       string        `mapstructure:"script_path"`
	PythonModulePath string        `mapstructure:"python_module_path"`
}

type MCPConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	Transport   string `mapstructure:"transport"`
	Address     string `mapstructure:"address"`
	TLSCertFile string `mapstructure:"tls_cert_file"`
	TLSKeyFile  string `mapstructure:"tls_key_file"`
}

// QueryConfig holds settings related to query execution
type QueryConfig struct {
	PreferStreaming bool   `mapstructure:"prefer_streaming"`
	ExecutionMode   string `mapstructure:"execution_mode"`
}
