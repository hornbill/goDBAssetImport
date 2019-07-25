package main

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"time"

	apiLib "github.com/hornbill/goApiLib"
	"github.com/hornbill/pb"
)

//getAssetClass -- Get Asset Class & Type ID from Asset Type Name
func getAssetClass(confAssetType string) (assetClass string, assetType int) {
	espXmlmc := apiLib.NewXmlmcInstance(SQLImportConf.InstanceID)
	espXmlmc.SetAPIKey(SQLImportConf.APIKey)
	espXmlmc.SetParam("application", appServiceManager)
	espXmlmc.SetParam("entity", "AssetsTypes")
	espXmlmc.OpenElement("searchFilter")
	espXmlmc.SetParam("column", "h_name")
	espXmlmc.SetParam("value", confAssetType)
	espXmlmc.SetParam("matchType", "exact")
	espXmlmc.CloseElement("searchFilter")
	espXmlmc.SetParam("maxResults", "1")
	var XMLSTRING = espXmlmc.GetParam()
	XMLGetMeta, xmlmcErr := espXmlmc.Invoke("data", "entityBrowseRecords2")
	if xmlmcErr != nil {
		logger(4, "API Call failed when retrieving Asset Class:"+fmt.Sprintf("%v", xmlmcErr), false)
		logger(1, "API XML: "+XMLSTRING, false)
	}

	var xmlRespon xmlmcTypeListResponse
	err := xml.Unmarshal([]byte(XMLGetMeta), &xmlRespon)
	if err != nil {
		logger(4, "Could not get Asset Class and Type. Please check AssetType within your configuration file:"+fmt.Sprintf("%v", err), true)
		logger(1, "API XML: "+XMLSTRING, false)
	} else {
		assetClass = xmlRespon.Params.RowData.Row.TypeClass
		assetType = xmlRespon.Params.RowData.Row.TypeID
	}
	return
}

//processAssets -- Processes Assets from Asset Map
//--If asset already exists on the instance, update
//--If asset doesn't exist, create
func processAssets(arrAssets []map[string]interface{}, assetIdentifier assetIdentifierStruct) {
	bar := pb.StartNew(len(arrAssets))
	logger(1, "Processing Assets", false)

	//Get the identity of the AssetID field from the config
	assetIDIdent := fmt.Sprintf("%v", assetIdentifier.DBColumn)
	debugLog("Asset Identifier:", assetIdentifier.DBColumn, assetIDIdent)
	//-- Loop each asset
	maxGoroutinesGuard := make(chan struct{}, maxGoroutines)

	for _, assetRecord := range arrAssets {
		maxGoroutinesGuard <- struct{}{}
		worker.Add(1)
		assetMap := assetRecord
		//Get the asset ID for the current record
		assetID := fmt.Sprintf("%s", assetMap[assetIDIdent])

		debugLog("Asset ID:", assetID)

		espXmlmc := apiLib.NewXmlmcInstance(SQLImportConf.InstanceID)
		espXmlmc.SetAPIKey(SQLImportConf.APIKey)
		go func() {
			defer worker.Done()
			time.Sleep(1 * time.Millisecond)
			mutexBar.Lock()
			bar.Increment()
			mutexBar.Unlock()

			var boolUpdate = false
			boolUpdate, searchSuccess, assetIDInstance := getAssetID(assetID, assetIdentifier, espXmlmc)
			debugLog("assetIDInstance:", assetIDInstance)
			//-- Update or Create Asset
			if searchSuccess {
				if boolUpdate {
					logger(1, "Update Asset: "+assetID, false)
					updateAsset(assetMap, assetIDInstance, espXmlmc)
				} else {
					logger(1, "Create Asset: "+assetID, false)
					createAsset(assetMap, espXmlmc)
				}
			} else {
				logger(4, "Asset search API call failed for asset with Unique ID: "+assetID, true)
			}
			<-maxGoroutinesGuard
		}()
	}
	worker.Wait()
	bar.FinishPrint("Processing Complete!")
}

//getAssetID -- Check if asset is on the instance
//-- Returns true, assetid if so
//-- Returns false, "" if not
func getAssetID(assetID string, assetIdentifier assetIdentifierStruct, espXmlmc *apiLib.XmlmcInstStruct) (bool, bool, string) {
	boolReturn := false
	boolSuccess := false
	returnAssetID := ""
	espXmlmc.SetParam("application", appServiceManager)
	espXmlmc.SetParam("entity", fmt.Sprintf("%v", assetIdentifier.Entity))
	espXmlmc.OpenElement("searchFilter")
	espXmlmc.SetParam("column", fmt.Sprintf("%v", assetIdentifier.EntityColumn))
	espXmlmc.SetParam("value", assetID)
	espXmlmc.SetParam("matchType", "exact")
	espXmlmc.CloseElement("searchFilter")
	espXmlmc.SetParam("maxResults", "1")
	var XMLSTRING = espXmlmc.GetParam()
	XMLAssetSearch, xmlmcErr := espXmlmc.Invoke("data", "entityBrowseRecords2")
	if xmlmcErr != nil {
		logger(4, "API Call failed when searching instance for existing Asset:"+fmt.Sprintf("%v", xmlmcErr), false)
		logger(1, "API Call XML: "+XMLSTRING, false)
	} else {
		var xmlRespon xmlmcAssetResponse
		err := xml.Unmarshal([]byte(XMLAssetSearch), &xmlRespon)
		if err != nil {
			logger(3, "Unable to Search for Asset: "+fmt.Sprintf("%v", err), true)
			logger(1, "API Call XML: "+XMLSTRING, false)
		} else {
			if xmlRespon.MethodResult != "ok" {
				logger(3, "Unable to Search for Asset: "+xmlRespon.State.ErrorRet, true)
				logger(1, "API Call XML: "+XMLSTRING, false)
			} else {
				boolSuccess = true
				returnAssetID = xmlRespon.Params.RowData.Row.AssetID
				//-- Check Response
				if returnAssetID != "" {
					boolReturn = true
				}
			}
		}
	}
	return boolReturn, boolSuccess, returnAssetID
}

// createAsset -- Creates Asset record from the passed through map data
func createAsset(u map[string]interface{}, espXmlmc *apiLib.XmlmcInstStruct) {
	//Get site ID
	siteID := ""
	siteNameMapping := fmt.Sprintf("%v", SQLImportConf.AssetGenericFieldMapping["h_site"])
	siteName := getFieldValue("h_site", siteNameMapping, u)
	if siteName != "" {
		siteIsInCache, SiteIDCache := siteInCache(siteName)
		//-- Check if we have cached the site already
		if siteIsInCache {
			siteID = strconv.Itoa(SiteIDCache)
		} else {
			siteIsOnInstance, SiteIDInstance := searchSite(siteName, espXmlmc)
			//-- If Returned set output
			if siteIsOnInstance {
				siteID = strconv.Itoa(SiteIDInstance)
			}
		}
	}
	debugLog("Site Mapping:", siteNameMapping, ":", siteName, ":", siteID)

	//Get Company ID
	companyID := ""
	companyNameMapping := fmt.Sprintf("%v", SQLImportConf.AssetGenericFieldMapping["h_company_name"])
	companyName := getFieldValue("h_company_name", companyNameMapping, u)
	if companyName != "" && companyName != "<nil>" {
		companyIsInCache, CompanyIDCache := groupInCache(companyName, 5)
		if companyIsInCache {
			companyID = CompanyIDCache
		} else {
			companyIsOnInstance, CompanyIDInstance := searchGroup(companyName, 5, espXmlmc)
			if companyIsOnInstance {
				companyID = CompanyIDInstance
			}
		}
	}
	debugLog("Company Mapping:", companyNameMapping, ":", companyName, ":", companyID)

	//Get Owned By name
	ownedByName := ""
	ownedByURN := ""
	ownedByMapping := fmt.Sprintf("%v", SQLImportConf.AssetGenericFieldMapping["h_owned_by"])
	ownedByID := getFieldValue("h_owned_by", ownedByMapping, u)
	if ownedByID != "" && ownedByID != "<nil>" {
		ownedByIsInCache, ownedByNameCache := customerInCache(ownedByID)
		//-- Check if we have cached the customer already
		if ownedByIsInCache {
			ownedByName = ownedByNameCache
		} else {
			ownedByIsOnInstance, ownedByNameInstance := searchCustomer(ownedByID, espXmlmc)
			//-- If Returned set output
			if ownedByIsOnInstance {
				ownedByName = ownedByNameInstance
			}
		}
	}
	if ownedByName != "" {
		ownedByURN = "urn:sys:0:" + ownedByName + ":" + ownedByID
	}
	debugLog("Owned By Mapping:", ownedByMapping, ":", ownedByID, ":", ownedByName, ":", ownedByURN)

	//Get Used By name
	usedByName := ""
	usedByURN := ""
	usedByMapping := fmt.Sprintf("%v", SQLImportConf.AssetGenericFieldMapping["h_used_by"])
	usedByID := getFieldValue("h_used_by", usedByMapping, u)
	if usedByID != "" && usedByID != "<nil>" {
		usedByIsInCache, usedByNameCache := customerInCache(usedByID)
		//-- Check if we have cached the customer already
		if usedByIsInCache {
			usedByName = usedByNameCache
		} else {
			usedByIsOnInstance, usedByNameInstance := searchCustomer(usedByID, espXmlmc)
			//-- If Returned set output
			if usedByIsOnInstance {
				usedByName = usedByNameInstance
			}
		}
	}
	if usedByName != "" {
		usedByURN = "urn:sys:0:" + usedByName + ":" + usedByID
	}
	debugLog("Used By Mapping:", usedByMapping, ":", usedByID, ":", usedByName, ":", usedByURN)

	//Last Logged On By
	lastLoggedOnByURN := ""
	lastLoggedOnByName := ""
	lastLoggedOnUserMapping := fmt.Sprintf("%v", SQLImportConf.AssetTypeFieldMapping["h_last_logged_on_user"])
	lastLoggedOnByID := getFieldValue("h_last_logged_on_user", lastLoggedOnUserMapping, u)
	if lastLoggedOnUserMapping != "" && lastLoggedOnByID != "" && lastLoggedOnByID != "<nil>" {
		lastLoggedOnByIsInCache, lastLoggedOnByNameCache := customerInCache(lastLoggedOnByID)
		//-- Check if we have cached the customer already
		if lastLoggedOnByIsInCache {
			lastLoggedOnByName = lastLoggedOnByNameCache
			lastLoggedOnByURN = "urn:sys:0:" + lastLoggedOnByNameCache + ":" + lastLoggedOnByID
		} else {
			lastLoggedOnByIsOnInstance, lastLoggedOnByNameInstance := searchCustomer(lastLoggedOnByID, espXmlmc)
			//-- If Returned set output
			if lastLoggedOnByIsOnInstance {
				lastLoggedOnByName = lastLoggedOnByNameInstance
				lastLoggedOnByURN = "urn:sys:0:" + lastLoggedOnByNameInstance + ":" + lastLoggedOnByID
			}
		}
	}
	debugLog("Last Logged On Mapping:", lastLoggedOnUserMapping, ":", lastLoggedOnByID, ":", lastLoggedOnByName, ":", lastLoggedOnByURN)

	//Get/Set params from map stored against FieldMapping
	espXmlmc.SetParam("application", appServiceManager)
	espXmlmc.SetParam("entity", "Asset")
	espXmlmc.SetParam("returnModifiedData", "true")
	espXmlmc.OpenElement("primaryEntityData")
	espXmlmc.OpenElement("record")
	//Set Class & TypeID
	espXmlmc.SetParam("h_class", AssetClass)
	espXmlmc.SetParam("h_type", strconv.Itoa(AssetTypeID))

	espXmlmc.SetParam("h_last_updated", APITimeNow)
	espXmlmc.SetParam("h_last_updated_by", "Import - Add")

	//Get asset field mapping
	debugLog("Asset Field Mapping")
	for k, v := range SQLImportConf.AssetGenericFieldMapping {
		strMapping := fmt.Sprintf("%v", v)
		value := getFieldValue(k, strMapping, u)
		debugLog(k, ":", strMapping, ":", value)

		if k == "h_used_by" && usedByName != "" && usedByURN != "" {
			espXmlmc.SetParam("h_used_by", usedByURN)
			espXmlmc.SetParam("h_used_by_name", usedByName)
		}
		if k == "h_owned_by" && ownedByName != "" && ownedByURN != "" {
			espXmlmc.SetParam("h_owned_by", ownedByURN)
			espXmlmc.SetParam("h_owned_by_name", ownedByName)
		}
		if k == "h_site" && siteID != "" && siteName != "" {
			espXmlmc.SetParam("h_site", siteName)
			espXmlmc.SetParam("h_site_id", siteID)
		}
		if k == "h_company_name" && companyID != "" && companyName != "" {
			espXmlmc.SetParam("h_company_name", companyName)
			espXmlmc.SetParam("h_company_id", companyID)
		}
		if k != "h_site" &&
			k != "h_used_by" &&
			k != "h_owned_by" &&
			k != "h_company_name" &&
			strMapping != "" && value != "" {
			espXmlmc.SetParam(k, value)

		}
	}
	espXmlmc.CloseElement("record")
	espXmlmc.CloseElement("primaryEntityData")

	//Add extended asset type field mapping
	espXmlmc.OpenElement("relatedEntityData")
	//Set Class & TypeID
	espXmlmc.SetParam("relationshipName", "AssetClass")
	espXmlmc.SetParam("entityAction", "insert")
	espXmlmc.OpenElement("record")
	espXmlmc.SetParam("h_type", strconv.Itoa(AssetTypeID))
	debugLog("Asset Type Field Mapping")
	//Get asset field mapping
	for k, v := range SQLImportConf.AssetTypeFieldMapping {
		strMapping := fmt.Sprintf("%v", v)
		value := getFieldValue(k, strMapping, u)
		debugLog(k, ":", strMapping, ":", value)

		if k == "h_last_logged_on_user" && lastLoggedOnByURN != "" {
			espXmlmc.SetParam("h_last_logged_on_user", lastLoggedOnByURN)
		}
		if k != "h_last_logged_on_user" &&
			strMapping != "" &&
			value != "" {
			espXmlmc.SetParam(k, value)
		}
	}
	espXmlmc.CloseElement("record")
	espXmlmc.CloseElement("relatedEntityData")

	//-- Check for Dry Run
	if !configDryRun {
		var XMLSTRING = espXmlmc.GetParam()
		debugLog("Asset Create XML:", XMLSTRING)
		XMLCreate, xmlmcErr := espXmlmc.Invoke("data", "entityAddRecord")
		if xmlmcErr != nil {
			logger(4, "Error running entityAddRecord API for createAsset:"+fmt.Sprintf("%v", xmlmcErr), false)
			logger(1, "API Call XML: "+XMLSTRING, false)
			return
		}
		var xmlRespon xmlmcUpdateResponse
		debugLog("API Call Response:", XMLCreate)
		err := xml.Unmarshal([]byte(XMLCreate), &xmlRespon)
		if err != nil {
			mutexCounters.Lock()
			counters.createskipped++
			mutexCounters.Unlock()
			logger(4, "Unable to read response from Hornbill instance from entityAddRecord API for createAsset:"+fmt.Sprintf("%v", err), false)
			logger(1, "API Call XML: "+XMLSTRING, false)
			return
		}
		if xmlRespon.MethodResult != "ok" {
			logger(3, "Unable to add asset: "+xmlRespon.State.ErrorRet, false)
			logger(1, "API Call XML: "+XMLSTRING, false)
			mutexCounters.Lock()
			counters.createskipped++
			mutexCounters.Unlock()
		} else {
			mutexCounters.Lock()
			counters.created++
			mutexCounters.Unlock()
			assetID := xmlRespon.UpdatedCols.AssetPK
			//Now add asset URN
			espXmlmc.SetParam("application", "com.hornbill.servicemanager")
			espXmlmc.SetParam("entity", "Asset")
			espXmlmc.OpenElement("primaryEntityData")
			espXmlmc.OpenElement("record")
			espXmlmc.SetParam("h_pk_asset_id", assetID)
			espXmlmc.SetParam("h_asset_urn", "urn:sys:entity:com.hornbill.servicemanager:Asset:"+assetID)
			espXmlmc.CloseElement("record")
			espXmlmc.CloseElement("primaryEntityData")
			XMLSTRING = espXmlmc.GetParam()
			XMLUpdate, xmlmcErr := espXmlmc.Invoke("data", "entityUpdateRecord")
			if xmlmcErr != nil {
				logger(4, "API Call failed when Updating Asset URN:"+fmt.Sprintf("%v", xmlmcErr), false)
				return
			}
			var xmlRespon xmlmcResponse

			err := xml.Unmarshal([]byte(XMLUpdate), &xmlRespon)
			if err != nil {
				logger(4, "Unable to read response from Hornbill instance when Updating Asset URN:"+fmt.Sprintf("%v", err), false)
				return
			}
			if xmlRespon.MethodResult != "ok" {
				logger(3, "Unable to update Asset URN: "+xmlRespon.State.ErrorRet, false)
				logger(1, "API Call XML: "+XMLSTRING, false)
				return
			}
			return
		}
	} else {
		//-- DEBUG XML TO LOG FILE
		var XMLSTRING = espXmlmc.GetParam()
		logger(1, "Asset Create XML "+XMLSTRING, false)
		mutexCounters.Lock()
		counters.createskipped++
		mutexCounters.Unlock()
		espXmlmc.ClearParam()
	}
}

// updateAsset -- Updates Asset record from the passed through map data and asset ID
func updateAsset(u map[string]interface{}, strAssetID string, espXmlmc *apiLib.XmlmcInstStruct) bool {

	boolRecordUpdated := false
	//Get site ID
	siteID := ""
	siteNameMapping := fmt.Sprintf("%v", SQLImportConf.AssetGenericFieldMapping["h_site"])
	siteName := getFieldValue("h_site", siteNameMapping, u)
	if siteName != "" && siteName != "<nil>" {
		siteIsInCache, SiteIDCache := siteInCache(siteName)
		//-- Check if we have cached the site already
		if siteIsInCache {
			siteID = strconv.Itoa(SiteIDCache)
		} else {
			siteIsOnInstance, SiteIDInstance := searchSite(siteName, espXmlmc)
			//-- If Returned set output
			if siteIsOnInstance {
				siteID = strconv.Itoa(SiteIDInstance)
			}
		}
	}
	debugLog("Site Mapping:", siteNameMapping, ":", siteName, ":", siteID)

	//Get Company ID
	companyID := ""
	companyNameMapping := fmt.Sprintf("%v", SQLImportConf.AssetGenericFieldMapping["h_company_name"])
	companyName := getFieldValue("h_company_name", companyNameMapping, u)
	if companyName != "" && companyName != "<nil>" {
		companyIsInCache, CompanyIDCache := groupInCache(companyName, 5)
		if companyIsInCache {
			companyID = CompanyIDCache
		} else {
			companyIsOnInstance, CompanyIDInstance := searchGroup(companyName, 5, espXmlmc)
			if companyIsOnInstance {
				companyID = CompanyIDInstance
			}
		}
	}
	debugLog("Company Mapping:", companyNameMapping, ":", companyName, ":", companyID)

	//Get Owned By name
	ownedByName := ""
	ownedByURN := ""
	ownedByMapping := fmt.Sprintf("%v", SQLImportConf.AssetGenericFieldMapping["h_owned_by"])
	ownedByID := getFieldValue("h_owned_by", ownedByMapping, u)
	if ownedByID != "" && ownedByID != "<nil>" {
		ownedByIsInCache, ownedByNameCache := customerInCache(ownedByID)
		//-- Check if we have cached the customer already
		if ownedByIsInCache {
			ownedByName = ownedByNameCache
		} else {
			ownedByIsOnInstance, ownedByNameInstance := searchCustomer(ownedByID, espXmlmc)
			//-- If Returned set output
			if ownedByIsOnInstance {
				ownedByName = ownedByNameInstance
			}
		}
	}
	if ownedByName != "" {
		ownedByURN = "urn:sys:0:" + ownedByName + ":" + ownedByID
	}
	debugLog("Owned By Mapping:", ownedByMapping, ":", ownedByID, ":", ownedByName, ":", ownedByURN)

	//Get Used By name
	usedByName := ""
	usedByURN := ""
	usedByMapping := fmt.Sprintf("%v", SQLImportConf.AssetGenericFieldMapping["h_used_by"])
	usedByID := getFieldValue("h_used_by", usedByMapping, u)
	if usedByID != "" && usedByID != "<nil>" {
		usedByIsInCache, usedByNameCache := customerInCache(usedByID)
		//-- Check if we have cached the customer already
		if usedByIsInCache {
			usedByName = usedByNameCache
		} else {
			usedByIsOnInstance, usedByNameInstance := searchCustomer(usedByID, espXmlmc)
			//-- If Returned set output
			if usedByIsOnInstance {
				usedByName = usedByNameInstance
			}
		}
	}
	if usedByName != "" {
		usedByURN = "urn:sys:0:" + usedByName + ":" + usedByID
	}
	debugLog("Used By Mapping:", usedByMapping, ":", usedByID, ":", usedByName, ":", usedByURN)

	//Last Logged On By
	lastLoggedOnByURN := ""
	lastLoggedOnByName := ""
	lastLoggedOnUserMapping := fmt.Sprintf("%v", SQLImportConf.AssetTypeFieldMapping["h_last_logged_on_user"])
	lastLoggedOnByID := getFieldValue("h_last_logged_on_user", lastLoggedOnUserMapping, u)
	if lastLoggedOnUserMapping != "" && lastLoggedOnByID != "" && lastLoggedOnByID != "<nil>" {
		lastLoggedOnByIsInCache, lastLoggedOnByNameCache := customerInCache(lastLoggedOnByID)
		//-- Check if we have cached the customer already
		if lastLoggedOnByIsInCache {
			lastLoggedOnByName = lastLoggedOnByNameCache
			lastLoggedOnByURN = "urn:sys:0:" + lastLoggedOnByNameCache + ":" + lastLoggedOnByID
		} else {
			lastLoggedOnByIsOnInstance, lastLoggedOnByNameInstance := searchCustomer(lastLoggedOnByID, espXmlmc)
			lastLoggedOnByName = lastLoggedOnByNameInstance
			//-- If Returned set output
			if lastLoggedOnByIsOnInstance {
				lastLoggedOnByURN = "urn:sys:0:" + lastLoggedOnByNameInstance + ":" + lastLoggedOnByID
			}
		}
	}
	debugLog("Last Logged On Mapping:", lastLoggedOnUserMapping, ":", lastLoggedOnByID, ":", lastLoggedOnByName, ":", lastLoggedOnByURN)

	//Get/Set params from map stored against FieldMapping
	espXmlmc.SetParam("application", appServiceManager)
	espXmlmc.SetParam("entity", "Asset")
	espXmlmc.SetParam("returnModifiedData", "true")
	espXmlmc.OpenElement("primaryEntityData")
	espXmlmc.OpenElement("record")
	espXmlmc.SetParam("h_pk_asset_id", strAssetID)
	espXmlmc.SetParam("h_asset_urn", "urn:sys:entity:com.hornbill.servicemanager:Asset:"+strAssetID)
	debugLog("Asset Field Mapping")
	//Get asset field mapping
	for k, v := range SQLImportConf.AssetGenericFieldMapping {
		strMapping := fmt.Sprintf("%v", v)
		value := getFieldValue(k, strMapping, u)
		debugLog(k, ":", strMapping, ":", value)

		if k == "h_used_by" && usedByName != "" && usedByURN != "" {
			espXmlmc.SetParam("h_used_by", usedByURN)
			espXmlmc.SetParam("h_used_by_name", usedByName)
		}
		if k == "h_owned_by" && ownedByName != "" && ownedByURN != "" {
			espXmlmc.SetParam("h_owned_by", ownedByURN)
			espXmlmc.SetParam("h_owned_by_name", ownedByName)
		}
		if k == "h_site" && siteID != "" && siteName != "" {
			espXmlmc.SetParam("h_site", siteName)
			espXmlmc.SetParam("h_site_id", siteID)
		}
		if k == "h_company_name" && companyID != "" && companyName != "" {
			espXmlmc.SetParam("h_company_name", companyName)
			espXmlmc.SetParam("h_company_id", companyID)
		}
		if k != "h_site" &&
			k != "h_used_by" &&
			k != "h_owned_by" &&
			k != "h_company_name" &&
			strMapping != "" &&
			value != "" {
			espXmlmc.SetParam(k, value)
		}
	}

	espXmlmc.CloseElement("record")
	espXmlmc.CloseElement("primaryEntityData")

	var XMLSTRING = espXmlmc.GetParam()
	//-- Check for Dry Run
	if !configDryRun {
		debugLog("Asset Update XML:", XMLSTRING)
		XMLUpdate, xmlmcErr := espXmlmc.Invoke("data", "entityUpdateRecord")
		if xmlmcErr != nil {
			logger(4, "API Call failed when Updating Asset:"+fmt.Sprintf("%v", xmlmcErr), false)
			logger(1, "API Call XML: "+XMLSTRING, false)
			mutexCounters.Lock()
			counters.updatedSkipped++
			mutexCounters.Unlock()
			return false
		}

		var xmlRespon xmlmcUpdateResponse

		err := xml.Unmarshal([]byte(XMLUpdate), &xmlRespon)
		if err != nil {
			logger(4, "Unable to read response from Hornbill instance when Updating Asset:"+fmt.Sprintf("%v", err), false)
			logger(1, "API Call XML: "+XMLSTRING, false)
			mutexCounters.Lock()
			counters.updatedSkipped++
			mutexCounters.Unlock()
			return false
		}
		if xmlRespon.MethodResult != "ok" && xmlRespon.State.ErrorRet != "There are no values to update" {
			logger(3, "Unable to Update Asset: "+xmlRespon.State.ErrorRet, false)
			logger(1, "API Call XML: "+XMLSTRING, false)
			mutexCounters.Lock()
			counters.updatedSkipped++
			mutexCounters.Unlock()
			return false
		}

		if len(xmlRespon.UpdatedCols.ColList) > 0 {
			boolRecordUpdated = true
		}

		//-- now process extended record data
		espXmlmc.SetParam("application", appServiceManager)
		espXmlmc.SetParam("entity", "Asset")
		espXmlmc.SetParam("returnModifiedData", "true")
		espXmlmc.OpenElement("primaryEntityData")
		espXmlmc.OpenElement("record")
		espXmlmc.SetParam("h_pk_asset_id", strAssetID)
		espXmlmc.CloseElement("record")
		espXmlmc.CloseElement("primaryEntityData")
		//Add extended asset type field mapping
		espXmlmc.OpenElement("relatedEntityData")
		//Set Class & TypeID
		espXmlmc.SetParam("relationshipName", "AssetClass")
		espXmlmc.SetParam("entityAction", "update")
		espXmlmc.OpenElement("record")
		espXmlmc.SetParam("h_pk_asset_id", strAssetID)
		debugLog("Asset Field Mapping")
		//Get asset field mapping
		for k, v := range SQLImportConf.AssetTypeFieldMapping {
			strMapping := fmt.Sprintf("%v", v)
			value := getFieldValue(k, strMapping, u)
			debugLog(k, ":", strMapping, ":", value)

			if k == "h_last_logged_on_user" && lastLoggedOnByURN != "" {
				espXmlmc.SetParam("h_last_logged_on_user", lastLoggedOnByURN)
			}
			if k != "h_last_logged_on_user" && strMapping != "" && value != "" {
				espXmlmc.SetParam(k, value)
			}
		}
		espXmlmc.CloseElement("record")
		espXmlmc.CloseElement("relatedEntityData")
		XMLMCRequest := espXmlmc.GetParam()
		debugLog("Asset Extended Update XML:", XMLMCRequest)
		XMLUpdateExt, xmlmcErrExt := espXmlmc.Invoke("data", "entityUpdateRecord")
		if xmlmcErrExt != nil {
			logger(4, "API Call failed when Updating Asset Extended Details:"+fmt.Sprintf("%v", xmlmcErrExt), false)
			logger(1, "API Call XML: "+XMLSTRING, false)
			mutexCounters.Lock()
			counters.updatedSkipped++
			mutexCounters.Unlock()
			return false
		}
		var xmlResponExt xmlmcUpdateResponse

		err = xml.Unmarshal([]byte(XMLUpdateExt), &xmlResponExt)
		if err != nil {
			logger(4, "Unable to read response from Hornbill instance when Updating Asset Extended Details:"+fmt.Sprintf("%v", err), false)
			logger(1, "API Call XML: "+XMLSTRING, false)
			mutexCounters.Lock()
			counters.updatedSkipped++
			mutexCounters.Unlock()
			return false
		}
		if xmlResponExt.MethodResult != "ok" && xmlResponExt.State.ErrorRet != "There are no values to update" {
			logger(3, "Unable to Update Asset Extended Details: "+xmlResponExt.State.ErrorRet, false)
			logger(1, "API Call XML: "+XMLSTRING, false)
			mutexCounters.Lock()
			counters.updatedSkipped++
			mutexCounters.Unlock()
			return false
		}

		if len(xmlResponExt.UpdatedCols.ColList) > 0 {
			boolRecordUpdated = true
		}

		if !boolRecordUpdated {
			mutexCounters.Lock()
			counters.updatedSkipped++
			mutexCounters.Unlock()
		} else {
			//-- Asset Updated!
			//-- Need to run another update against the Asset for LAST UPDATED and LAST UPDATE BY!
			espXmlmc.SetParam("application", appServiceManager)
			espXmlmc.SetParam("entity", "Asset")
			espXmlmc.OpenElement("primaryEntityData")
			espXmlmc.OpenElement("record")
			espXmlmc.SetParam("h_pk_asset_id", strAssetID)
			espXmlmc.SetParam("h_last_updated", APITimeNow)
			espXmlmc.SetParam("h_last_updated_by", "Import - Update")
			espXmlmc.CloseElement("record")
			espXmlmc.CloseElement("primaryEntityData")
			var XMLSTRING = espXmlmc.GetParam()
			debugLog("Asset Update LAST UPDATE XML:", XMLSTRING)
			XMLUpdate, xmlmcErr := espXmlmc.Invoke("data", "entityUpdateRecord")
			if xmlmcErr != nil {
				logger(4, "API Call failed when setting Last Updated values:"+fmt.Sprintf("%v", xmlmcErr), false)
				logger(1, "Asset Last Updated XML: "+XMLSTRING, false)
			}
			var xmlRespon xmlmcResponse
			err := xml.Unmarshal([]byte(XMLUpdate), &xmlRespon)
			if err != nil {
				logger(4, "Unable to read response from Hornbill instance when setting Last Updated values:"+fmt.Sprintf("%v", err), false)
				logger(1, "Asset Last Updated XML: "+XMLSTRING, false)
			}
			if xmlRespon.MethodResult != "ok" && xmlRespon.State.ErrorRet != "There are no values to update" {
				logger(3, "Unable to set Last Updated details for asset: "+xmlRespon.State.ErrorRet, false)
				logger(1, "Asset Last Updated XML: "+XMLSTRING, false)
			}
			mutexCounters.Lock()
			counters.updated++
			mutexCounters.Unlock()
		}

	} else {
		//-- Inc Counter
		mutexCounters.Lock()
		counters.updatedSkipped++
		mutexCounters.Unlock()
		logger(1, "Asset Update XML "+XMLSTRING, false)
		espXmlmc.ClearParam()
	}
	return true
}