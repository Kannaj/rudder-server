package event_schema

import (
	"encoding/json"
	"github.com/ory/dockertest/v3"
	"github.com/rudderlabs/rudder-server/admin"
	"github.com/rudderlabs/rudder-server/config"
	"github.com/rudderlabs/rudder-server/jobsdb"
	migrator "github.com/rudderlabs/rudder-server/services/sql-migrator"
	"github.com/rudderlabs/rudder-server/services/stats"
	"github.com/rudderlabs/rudder-server/testhelper"
	"github.com/rudderlabs/rudder-server/testhelper/destination"
	"github.com/rudderlabs/rudder-server/utils/logger"
	"log"
	"os"
	"testing"
)

var pgResource *destination.PostgresResource

func TestMain(m *testing.M) {
	log.Println("Initialize the database here with the necessary table structures.")

	pool, err := dockertest.NewPool("")
	if err != nil {
		log.Fatalf("Unable to bring up pool for creating containers")
	}

	cleanup := &testhelper.Cleanup{}
	pgResource, err = destination.SetupPostgres(pool, cleanup)
	if err != nil {
		log.Fatalf("Unable to setup the postgres container")
	}

	defer cleanup.Run()

	mg := &migrator.Migrator{
		Handle:                     pgResource.DB,
		MigrationsTable:            "node_migrations",
		ShouldForceSetLowerVersion: config.GetBool("SQLMigrator.forceSetLowerVersion", true),
	}

	err = mg.Migrate("node")
	if err != nil {
		log.Fatalf("Unable to run the migrations for the node.")
	}

	// Load self configuration.
	config.Load()

	logger.Init() // Initialize the logger
	Init2()
	Init()

	// Setup the supporting services like stats and jobsdb
	stats.Setup()
	jobsDBInit(pgResource)

	// Finally run the testcase
	m.Run()
}

func jobsDBInit(pgResource *destination.PostgresResource) {

	// Setup env for jobsdb.
	os.Setenv("JOBS_DB_HOST", pgResource.Host)
	os.Setenv("JOBS_DB_USER", pgResource.User)
	os.Setenv("JOBS_DB_PASSWORD", pgResource.Password)
	os.Setenv("JOBS_DB_DB_NAME", pgResource.Database)
	os.Setenv("JOBS_DB_PORT", pgResource.Port)

	admin.Init()
	jobsdb.Init()
	jobsdb.Init3()
	jobsdb.Init2()
}

func BenchmarkEventSchemaHandleEvent(b *testing.B) {
	b.Log("Benchmarking the handling event of event schema")

	var eventStr = `{"batch": [{"type": "track", "event": "Demo Track", "sentAt": "2019-08-12T05:08:30.909Z", "channel": "android-sdk", "context": {"app": {"name": "RudderAndroidClient", "build": "1", "version": "1.0", "namespace": "com.rudderlabs.android.sdk"}, "device": {"id": "49e4bdd1c280bc00", "name": "generic_x86", "model": "Android SDK built for x86", "manufacturer": "Google"}, "locale": "en-US", "screen": {"width": 1080, "height": 1794, "density": 420}, "traits": {"anonymousId": "49e4bdd1c280bc00"}, "library": {"name": "com.rudderstack.android.sdk.core"}, "network": {"carrier": "Android"}, "user_agent": "Dalvik/2.1.0 (Linux; U; Android 9; Android SDK built for x86 Build/PSR1.180720.075)"}, "rudderId": "90ca6da0-292e-4e79-9880-f8009e0ae4a3", "messageId": "a82717d0-b939-47bd-9592-59bbbea66c1a", "properties": {"label": "Demo Label", "value": 5, "testMap": {"t1": "a", "t2": 4}, "category": "Demo Category", "floatVal": 4.501, "testArray": [{"id": "elem1", "value": "e1"}, {"id": "elem2", "value": "e2"}]}, "anonymousId": "anon_id", "integrations": {"All": true}, "originalTimestamp": "2019-08-12T05:08:30.909Z"}], "writeKey": "29grCPTQ6XrVOSK76M7vfE5BWjb", "requestIP": "127.0.0.1", "receivedAt": "2022-06-15T13:51:08.754+05:30"}`
	var eventPayload EventPayloadT
	if err := json.Unmarshal([]byte(eventStr), &eventPayload); err != nil {
		b.Errorf("Invalid request payload for unmarshalling: %v", err.Error())
		return
	}

	manager := EventSchemaManagerT{
		dbHandle:             pgResource.DB,
		disableInMemoryCache: false,
		eventModelMap:        EventModelMapT{},
		schemaVersionMap:     SchemaVersionMapT{},
	}

	for i := 0; i < b.N; i++ {
		eventPayload.Batch[0]["dynamicProperty"] = i
		manager.handleEvent("", eventPayload.Batch[0])
	}

	// flush the event schemas to the database.
	err := manager.flushEventSchemasToDB()
	if err != nil {
		b.Errorf("Unable to flush events back to database")
	}
}
