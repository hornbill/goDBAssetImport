package main

//----- Packages -----
import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/fatih/color"
	"github.com/tcnksm/go-latest"

	"time"
	//SQL Drivers
	_ "github.com/alexbrainman/odbc" //ODBC Driver
	_ "github.com/denisenkom/go-mssqldb"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/hornbill/mysql320" //MySQL v3.2.0 to v5 driver - Provides SWSQL (MySQL 4.0.16) support
)

//----- Main Function -----
func main() {
	//-- Start Time for Duration
	startTime = time.Now()

	//-- Grab Flags
	flag.StringVar(&configFileName, "file", "conf.json", "Name of Configuration File To Load")
	flag.BoolVar(&configDebug, "debug", false, "Output additional debug information to the log")
	flag.BoolVar(&configDryRun, "dryrun", false, "Allow the Import to run without Creating or Updating Assets")
	flag.StringVar(&configMaxRoutines, "concurrent", "1", "Maximum number of Assets to import concurrently.")
	flag.BoolVar(&configVersion, "version", false, "Return version and end")
	flag.Parse()

	//-- If configVersion just output version number and die
	if configVersion {
		fmt.Printf("%v \n", version)
		return
	}

	//--
	//-- Load Configuration File Into Struct
	SQLImportConf = loadConfig()
	if SQLImportConf.LogSizeBytes > 0 {
		maxLogFileSize = SQLImportConf.LogSizeBytes
	}
	//XMLMC session to perform local caching of instance records with
	initXMLMC()

	checkVersion()
	//-- Output
	logger(1, "---- XMLMC Database Asset Import Utility v"+version+" ----", true, true)
	logger(1, "Flag - Config File "+configFileName, true, true)
	logger(1, "Flag - Dry Run "+fmt.Sprintf("%v", configDryRun), true, true)
	logger(1, "Flag - Concurrent "+configMaxRoutines, true, true)

	//Check maxGoroutines for valid value
	maxRoutines, err := strconv.Atoi(configMaxRoutines)
	if err != nil {
		color.Red("Unable to convert maximum concurrency of [" + configMaxRoutines + "] to type INT for processing")
		return
	}
	maxGoroutines = maxRoutines

	if maxGoroutines < 1 || maxGoroutines > 10 {
		color.Red("The maximum concurrent assets allowed is between 1 and 10 (inclusive).\n\n")
		color.Red("You have selected " + configMaxRoutines + ". Please try again, with a valid value against ")
		color.Red("the -concurrent switch.")
		return
	}

	//Set SWSQLDriver to mysql320
	if SQLImportConf.SQLConf.Driver == "swsql" {
		SQLImportConf.SQLConf.Driver = "mysql320"
	}

	processCaching()

	//Build DB connection string
	connString = buildConnectionString()
	if connString == "" {
		logger(4, " [DATABASE] Database Connection String Empty. Check the SQLConf section of your configuration.", true, true)
		return
	}

	//Get asset types, process accordingly
	BaseSQLQuery = SQLImportConf.SQLConf.Query
	for _, v := range SQLImportConf.AssetTypes {
		StrAssetType = fmt.Sprintf("%v", v.AssetType)
		StrSQLAppend = fmt.Sprintf("%v", v.Query)
		//Set Asset Class & Type vars from instance
		AssetClass, AssetTypeID = getAssetClass(StrAssetType)
		v.TypeID = AssetTypeID
		v.Class = AssetClass
		debugLog(nil, "Asset Type and Class:", StrAssetType, strconv.Itoa(AssetTypeID), AssetClass)

		//-- Query Database
		var boolSQLAssets, arrAssets = queryAssets(StrSQLAppend, v)
		if boolSQLAssets && len(arrAssets) > 0 {
			//Cache instance asset records of class & type
			logger(1, "Caching "+v.AssetType+" Asset Records from Hornbill...", true, true)
			assetCount, err := getAssetCount(v, hornbillImport)
			if err != nil {
				logger(4, "Unable to count asset records: "+err.Error(), true, true)
				continue
			}
			var assetCache map[string]map[string]interface{}
			if assetCount > 0 {
				assetCache, err = getAssetRecords(assetCount, v, hornbillImport)
				if err != nil {
					logger(4, "Unable to cache asset records: "+err.Error(), true, true)
					continue
				}
			}
			//Process records returned by query & cache
			processAssets(arrAssets, assetCache, v)
		}
	}

	//-- End output
	logger(1, "Created: "+fmt.Sprintf("%d", counters.created), true, true)
	logger(1, "Create Skipped: "+fmt.Sprintf("%d", counters.createSkipped), true, true)
	logger(1, "Create Failed: "+fmt.Sprintf("%d", counters.createFailed), true, true)
	logger(1, "Updated: "+fmt.Sprintf("%d", counters.updated), true, true)
	logger(1, "Update Skipped: "+fmt.Sprintf("%d", counters.updateSkipped), true, true)
	logger(1, "Update Failed: "+fmt.Sprintf("%d", counters.updateFailed), true, true)
	logger(1, "Update Extended Record Skipped: "+fmt.Sprintf("%d", counters.updateRelatedSkipped), true, true)
	logger(1, "Update Extended Record Failed: "+fmt.Sprintf("%d", counters.updateRelatedFailed), true, true)
	logger(1, "Assets Software Inventory Skipped: "+fmt.Sprintf("%d", counters.softwareSkipped), true, true)
	logger(1, "Software Records Created: "+fmt.Sprintf("%d", counters.softwareCreated), true, true)
	logger(1, "Software Records Create Failed: "+fmt.Sprintf("%d", counters.softwareCreateFailed), true, true)
	logger(1, "Software Records Removed: "+fmt.Sprintf("%d", counters.softwareRemoved), true, true)
	logger(1, "Software Records Removal Failed: "+fmt.Sprintf("%d", counters.softwareRemoveFailed), true, true)

	//-- Show Time Takens
	logger(1, "Time Taken: "+fmt.Sprintf("%v", time.Since(startTime).Round(time.Second)), true, true)
	logger(1, "---- XMLMC Database Asset Import Complete ---- ", true, true)
}

//loadConfig -- Function to Load Configruation File
func loadConfig() sqlImportConfStruct {
	//-- Check Config File File Exists
	cwd, _ := os.Getwd()
	configurationFilePath := cwd + "/" + configFileName
	logger(1, "Loading Config File: "+configurationFilePath, false, false)
	if _, fileCheckErr := os.Stat(configurationFilePath); os.IsNotExist(fileCheckErr) {
		logger(4, "No Configuration File", true, false)
		os.Exit(102)
	}
	//-- Load Config File
	file, fileError := os.Open(configurationFilePath)
	//-- Check For Error Reading File
	if fileError != nil {
		logger(4, "Error Opening Configuration File: "+fmt.Sprintf("%v", fileError), true, false)
	}

	//-- New Decoder
	decoder := json.NewDecoder(file)
	//-- New Var based on SQLImportConf
	esqlConf := sqlImportConfStruct{}
	//-- Decode JSON
	err := decoder.Decode(&esqlConf)
	//-- Error Checking
	if err != nil {
		logger(4, "Error Decoding Configuration File: "+fmt.Sprintf("%v", err), true, false)
	}
	//-- Return New Congfig
	return esqlConf
}

func processCaching() {

	//only load if any of the user colums are set
	SQLImportConf.HornbillUserIDColumn = strings.ToLower(SQLImportConf.HornbillUserIDColumn)
	blnHasUserConfigured := false
	if val, ok := SQLImportConf.AssetGenericFieldMapping["h_owned_by"]; ok {
		if val != "" {
			blnHasUserConfigured = true
		}
	}
	if val, ok := SQLImportConf.AssetGenericFieldMapping["h_used_by"]; ok {
		if val != "" {
			blnHasUserConfigured = true
		}
	}
	if val, ok := SQLImportConf.AssetTypeFieldMapping["h_last_logged_on_user"]; ok {
		if val != "" {
			blnHasUserConfigured = true
		}
	}

	if blnHasUserConfigured {
		loadUsers()
	}

	//only load if site colum is configured
	if val, ok := SQLImportConf.AssetGenericFieldMapping["h_site"]; ok {
		if val != "" {
			loadSites()
		}
	}

	var queryGroups []string
	if val, ok := SQLImportConf.AssetGenericFieldMapping["h_company_name"]; ok {
		if val != "" {
			queryGroups = append(queryGroups, "company")
		}
	}
	if val, ok := SQLImportConf.AssetGenericFieldMapping["h_department_name"]; ok {
		if val != "" {
			queryGroups = append(queryGroups, "department")
		}
	}

	if len(queryGroups) > 0 {
		loadGroups(queryGroups)
	}

	getApplications()
}

//-- Check Latest
func checkVersion() {
	githubTag := &latest.GithubTag{
		Owner:      "hornbill",
		Repository: appName,
	}

	res, err := latest.Check(githubTag, version)
	if err != nil {
		msg := "Unable to check utility version against Github repository: " + err.Error()
		logger(4, msg, true, true)
		return
	}
	if res.Outdated {
		msg := "v" + version + " is not latest, you should upgrade to " + res.Current + " by downloading the latest package from: https://github.com/hornbill/" + appName + "/releases/tag/v" + res.Current
		logger(5, msg, true, true)
	}
}
