package coordinator

import (
	"context"
	"fmt"
	"time"

	"github.com/chroma-core/chroma/go/pkg/common"
	"github.com/chroma-core/chroma/go/pkg/sysdb/coordinator/model"
	"github.com/chroma-core/chroma/go/pkg/sysdb/metastore/db/dbmodel"
	"github.com/chroma-core/chroma/go/pkg/types"
	"github.com/chroma-core/chroma/go/shared/otel"
	"github.com/pingcap/log"
	"go.uber.org/zap"
)

// The catalog backed by databases using GORM.
type Catalog struct {
	metaDomain dbmodel.IMetaDomain
	txImpl     dbmodel.ITransaction
}

func NewTableCatalog(txImpl dbmodel.ITransaction, metaDomain dbmodel.IMetaDomain) *Catalog {
	return &Catalog{
		txImpl:     txImpl,
		metaDomain: metaDomain,
	}
}

func (tc *Catalog) ResetState(ctx context.Context) error {
	return tc.txImpl.Transaction(ctx, func(txCtx context.Context) error {
		err := tc.metaDomain.CollectionMetadataDb(txCtx).DeleteAll()
		if err != nil {
			log.Error("error reest collection metadata db", zap.Error(err))
			return err
		}
		err = tc.metaDomain.CollectionDb(txCtx).DeleteAll()
		if err != nil {
			log.Error("error reset collection db", zap.Error(err))
			return err
		}
		err = tc.metaDomain.SegmentMetadataDb(txCtx).DeleteAll()
		if err != nil {
			log.Error("error reset segment metadata db", zap.Error(err))
			return err
		}
		err = tc.metaDomain.SegmentDb(txCtx).DeleteAll()
		if err != nil {
			log.Error("error reset segment db", zap.Error(err))
			return err
		}

		err = tc.metaDomain.DatabaseDb(txCtx).DeleteAll()
		if err != nil {
			log.Error("error reset database db", zap.Error(err))
			return err
		}

		// TODO: default database and tenant should be pre-defined object
		err = tc.metaDomain.DatabaseDb(txCtx).Insert(&dbmodel.Database{
			ID:       types.NilUniqueID().String(),
			Name:     common.DefaultDatabase,
			TenantID: common.DefaultTenant,
		})
		if err != nil {
			log.Error("error inserting default database", zap.Error(err))
			return err
		}

		err = tc.metaDomain.TenantDb(txCtx).DeleteAll()
		if err != nil {
			log.Error("error reset tenant db", zap.Error(err))
			return err
		}
		err = tc.metaDomain.TenantDb(txCtx).Insert(&dbmodel.Tenant{
			ID:                 common.DefaultTenant,
			LastCompactionTime: time.Now().Unix(),
		})
		if err != nil {
			log.Error("error inserting default tenant", zap.Error(err))
			return err
		}

		return nil
	})
}

func (tc *Catalog) CreateDatabase(ctx context.Context, createDatabase *model.CreateDatabase, ts types.Timestamp) (*model.Database, error) {
	var result *model.Database

	// Check if database name is not empty
	if createDatabase.Name == "" {
		return nil, common.ErrDatabaseNameEmpty
	}

	// Check if tenant exists for the given tenant id
	tenants, err := tc.metaDomain.TenantDb(ctx).GetTenants(createDatabase.Tenant)
	if err != nil {
		log.Error("error getting tenants", zap.Error(err))
		return nil, err
	}
	if len(tenants) == 0 {
		log.Error("tenant not found", zap.Error(err))
		return nil, common.ErrTenantNotFound
	}

	err = tc.txImpl.Transaction(ctx, func(txCtx context.Context) error {
		dbDatabase := &dbmodel.Database{
			ID:       createDatabase.ID,
			Name:     createDatabase.Name,
			TenantID: createDatabase.Tenant,
			Ts:       ts,
		}
		err := tc.metaDomain.DatabaseDb(txCtx).Insert(dbDatabase)
		if err != nil {
			log.Error("error inserting database", zap.Error(err))
			return err
		}
		databaseList, err := tc.metaDomain.DatabaseDb(txCtx).GetDatabases(createDatabase.Tenant, createDatabase.Name)
		if err != nil {
			log.Error("error getting database", zap.Error(err))
			return err
		}
		result = convertDatabaseToModel(databaseList[0])
		return nil
	})
	if err != nil {
		log.Error("error creating database", zap.Error(err))
		return nil, err
	}
	log.Info("database created", zap.Any("database", result))
	return result, nil
}

func (tc *Catalog) GetDatabases(ctx context.Context, getDatabase *model.GetDatabase, ts types.Timestamp) (*model.Database, error) {
	databases, err := tc.metaDomain.DatabaseDb(ctx).GetDatabases(getDatabase.Tenant, getDatabase.Name)
	if err != nil {
		return nil, err
	}
	if len(databases) == 0 {
		return nil, common.ErrDatabaseNotFound
	}
	result := make([]*model.Database, 0, len(databases))
	for _, database := range databases {
		result = append(result, convertDatabaseToModel(database))
	}
	return result[0], nil
}

func (tc *Catalog) GetAllDatabases(ctx context.Context, ts types.Timestamp) ([]*model.Database, error) {
	databases, err := tc.metaDomain.DatabaseDb(ctx).GetAllDatabases()
	if err != nil {
		log.Error("error getting all databases", zap.Error(err))
		return nil, err
	}
	result := make([]*model.Database, 0, len(databases))
	for _, database := range databases {
		result = append(result, convertDatabaseToModel(database))
	}
	return result, nil
}

func (tc *Catalog) CreateTenant(ctx context.Context, createTenant *model.CreateTenant, ts types.Timestamp) (*model.Tenant, error) {
	var result *model.Tenant

	err := tc.txImpl.Transaction(ctx, func(txCtx context.Context) error {
		// TODO: createTenant has ts, don't need to pass in
		dbTenant := &dbmodel.Tenant{
			ID:                 createTenant.Name,
			Ts:                 ts,
			LastCompactionTime: time.Now().Unix(),
		}
		err := tc.metaDomain.TenantDb(txCtx).Insert(dbTenant)
		if err != nil {
			return err
		}
		tenantList, err := tc.metaDomain.TenantDb(txCtx).GetTenants(createTenant.Name)
		if err != nil {
			return err
		}
		result = convertTenantToModel(tenantList[0])
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (tc *Catalog) GetTenants(ctx context.Context, getTenant *model.GetTenant, ts types.Timestamp) (*model.Tenant, error) {
	tenants, err := tc.metaDomain.TenantDb(ctx).GetTenants(getTenant.Name)
	if err != nil {
		log.Error("error getting tenants", zap.Error(err))
		return nil, err
	}
	if (len(tenants)) == 0 {
		log.Error("tenant not found", zap.Error(err))
		return nil, common.ErrTenantNotFound
	}
	result := make([]*model.Tenant, 0, len(tenants))
	for _, tenant := range tenants {
		result = append(result, convertTenantToModel(tenant))
	}
	return result[0], nil
}

func (tc *Catalog) GetAllTenants(ctx context.Context, ts types.Timestamp) ([]*model.Tenant, error) {
	tenants, err := tc.metaDomain.TenantDb(ctx).GetAllTenants()
	if err != nil {
		log.Error("error getting all tenants", zap.Error(err))
		return nil, err
	}
	result := make([]*model.Tenant, 0, len(tenants))
	for _, tenant := range tenants {
		result = append(result, convertTenantToModel(tenant))
	}
	return result, nil
}

func (tc *Catalog) createCollectionImpl(txCtx context.Context, createCollection *model.CreateCollection, ts types.Timestamp) (*model.Collection, bool, error) {
	// insert collection
	databaseName := createCollection.DatabaseName
	tenantID := createCollection.TenantID
	databases, err := tc.metaDomain.DatabaseDb(txCtx).GetDatabases(tenantID, databaseName)
	if err != nil {
		log.Error("error getting database", zap.Error(err))
		return nil, false, err
	}
	if len(databases) == 0 {
		log.Error("database not found", zap.Error(err))
		return nil, false, common.ErrDatabaseNotFound
	}

	collectionName := createCollection.Name
	existing, err := tc.metaDomain.CollectionDb(txCtx).GetCollections(nil, &collectionName, tenantID, databaseName, nil, nil)
	if err != nil {
		log.Error("error getting collection", zap.Error(err))
		return nil, false, err
	}
	if len(existing) != 0 {
		if createCollection.GetOrCreate {
			collection := convertCollectionToModel(existing)[0]
			return collection, false, nil
		} else {
			return nil, false, common.ErrCollectionUniqueConstraintViolation
		}
	} else {
		// If collection is soft-deleted, then new collection will throw an error since name should be unique, so we need to rename it.
		isSoftDeleted, sdCollectionID, err := tc.metaDomain.CollectionDb(txCtx).CheckCollectionIsSoftDeleted(createCollection.Name, tenantID, databaseName)
		if err != nil {
			return nil, false, fmt.Errorf("failed to create collection: %w", err)
		}
		if isSoftDeleted {
			log.Info("new collection create request with same name as collection that was soft deleted", zap.Any("collection", createCollection))
			// Rename the soft deleted collection to a new name with timestamp
			err = tc.renameSoftDeletedCollection(txCtx, sdCollectionID, createCollection.Name, tenantID, databaseName)
			if err != nil {
				return nil, false, fmt.Errorf("failed to create collection: %w", err)
			}
		}
	}

	dbCollection := &dbmodel.Collection{
		ID:                   createCollection.ID.String(),
		Name:                 &createCollection.Name,
		ConfigurationJsonStr: &createCollection.ConfigurationJsonStr,
		Dimension:            createCollection.Dimension,
		DatabaseID:           databases[0].ID,
		Ts:                   ts,
		LogPosition:          0,
	}

	err = tc.metaDomain.CollectionDb(txCtx).Insert(dbCollection)
	if err != nil {
		log.Error("error inserting collection", zap.Error(err))
		return nil, false, err
	}
	// insert collection metadata
	metadata := createCollection.Metadata
	dbCollectionMetadataList := convertCollectionMetadataToDB(createCollection.ID.String(), metadata)
	if len(dbCollectionMetadataList) != 0 {
		err = tc.metaDomain.CollectionMetadataDb(txCtx).Insert(dbCollectionMetadataList)
		if err != nil {
			return nil, false, err
		}
	}
	// get collection
	collectionList, err := tc.metaDomain.CollectionDb(txCtx).GetCollections(types.FromUniqueID(createCollection.ID), nil, tenantID, databaseName, nil, nil)
	if err != nil {
		log.Error("error getting collection", zap.Error(err))
		return nil, false, err
	}
	result := convertCollectionToModel(collectionList)[0]
	return result, true, nil

}

func (tc *Catalog) CreateCollection(ctx context.Context, createCollection *model.CreateCollection, ts types.Timestamp) (*model.Collection, bool, error) {
	var result *model.Collection
	created := false
	err := tc.txImpl.Transaction(ctx, func(txCtx context.Context) error {
		var err error
		result, created, err = tc.createCollectionImpl(txCtx, createCollection, ts)
		return err
	})
	if err != nil {
		log.Error("error creating collection", zap.Error(err))
		return nil, false, err
	}
	log.Info("collection created", zap.Any("collection", result))
	return result, created, nil
}

func (tc *Catalog) GetCollections(ctx context.Context, collectionID types.UniqueID, collectionName *string, tenantID string, databaseName string, limit *int32, offset *int32) ([]*model.Collection, error) {
	tracer := otel.Tracer
	if tracer != nil {
		_, span := tracer.Start(ctx, "Catalog.GetCollections")
		defer span.End()
	}

	collectionAndMetadataList, err := tc.metaDomain.CollectionDb(ctx).GetCollections(types.FromUniqueID(collectionID), collectionName, tenantID, databaseName, limit, offset)
	if err != nil {
		return nil, err
	}
	collections := convertCollectionToModel(collectionAndMetadataList)
	return collections, nil
}

func (tc *Catalog) DeleteCollection(ctx context.Context, deleteCollection *model.DeleteCollection, softDelete bool) error {
	if softDelete {
		return tc.softDeleteCollection(ctx, deleteCollection)
	}
	return tc.hardDeleteCollection(ctx, deleteCollection)
}

func (tc *Catalog) hardDeleteCollection(ctx context.Context, deleteCollection *model.DeleteCollection) error {
	log.Info("hard deleting collection", zap.Any("deleteCollection", deleteCollection), zap.String("databaseName", deleteCollection.DatabaseName))
	return tc.txImpl.Transaction(ctx, func(txCtx context.Context) error {
		collectionID := deleteCollection.ID

		collectionEntry, err := tc.metaDomain.CollectionDb(txCtx).GetCollectionEntry(types.FromUniqueID(collectionID), &deleteCollection.DatabaseName)
		if err != nil {
			return err
		}
		if collectionEntry == nil {
			log.Info("collection not found during hard delete", zap.Any("deleteCollection", deleteCollection))
			return common.ErrCollectionDeleteNonExistingCollection
		}

		// Delete collection and collection metadata.
		collectionDeletedCount, err := tc.metaDomain.CollectionDb(txCtx).DeleteCollectionByID(collectionID.String())
		if err != nil {
			log.Error("error deleting collection during hard delete", zap.Error(err))
			return err
		}
		if collectionDeletedCount == 0 {
			log.Info("collection not found during hard delete", zap.Any("deleteCollection", deleteCollection))
			return common.ErrCollectionDeleteNonExistingCollection
		}
		// Delete collection metadata.
		collectionMetadataDeletedCount, err := tc.metaDomain.CollectionMetadataDb(txCtx).DeleteByCollectionID(collectionID.String())
		if err != nil {
			log.Error("error deleting collection metadata during hard delete", zap.Error(err))
			return err
		}
		// Delete segments.
		segments, err := tc.metaDomain.SegmentDb(txCtx).GetSegmentsByCollectionID(collectionID.String())
		if err != nil {
			log.Error("error getting segments during hard delete", zap.Error(err))
			return err
		}
		for _, segment := range segments {
			err = tc.metaDomain.SegmentDb(txCtx).DeleteSegmentByID(segment.ID)
			if err != nil {
				log.Error("error deleting segment during hard delete", zap.Error(err))
				return err
			}
			err = tc.metaDomain.SegmentMetadataDb(txCtx).DeleteBySegmentID(segment.ID)
			if err != nil {
				log.Error("error deleting segment metadata during hard delete", zap.Error(err))
				return err
			}
		}

		log.Info("collection hard deleted", zap.Any("collection", collectionID),
			zap.Int("collectionDeletedCount", collectionDeletedCount),
			zap.Int("collectionMetadataDeletedCount", collectionMetadataDeletedCount))
		return nil
	})
}

func (tc *Catalog) renameSoftDeletedCollection(ctx context.Context, collectionID string, collectionName string, tenantID string, databaseName string) error {
	log.Info("Renaming soft deleted collection", zap.String("collectionID", collectionID), zap.String("collectionName", collectionName), zap.Any("tenantID", tenantID), zap.String("databaseName", databaseName))
	// Generate new name with timestamp
	newName := fmt.Sprintf("deleted_%s_%d", collectionName, time.Now().Unix())

	dbCollection := &dbmodel.Collection{
		ID:        collectionID,
		Name:      &newName,
		IsDeleted: true,
	} // Not updating the timestamp or updated_at.

	err := tc.metaDomain.CollectionDb(ctx).Update(dbCollection)
	if err != nil {
		log.Error("rename soft deleted collection failed", zap.Error(err))
		return fmt.Errorf("collection rename failed due to update error: %w", err)
	}
	return nil
}

func (tc *Catalog) softDeleteCollection(ctx context.Context, deleteCollection *model.DeleteCollection) error {
	log.Info("Soft deleting collection", zap.Any("softDeleteCollection", deleteCollection))
	return tc.txImpl.Transaction(ctx, func(txCtx context.Context) error {
		// Check if collection exists
		collections, err := tc.metaDomain.CollectionDb(txCtx).GetCollections(types.FromUniqueID(deleteCollection.ID), nil, deleteCollection.TenantID, deleteCollection.DatabaseName, nil, nil)
		if err != nil {
			return err
		}
		if len(collections) == 0 {
			return common.ErrCollectionDeleteNonExistingCollection
		}

		dbCollection := &dbmodel.Collection{
			ID:        deleteCollection.ID.String(),
			IsDeleted: true,
			Ts:        deleteCollection.Ts,
			UpdatedAt: time.Now(),
		}
		err = tc.metaDomain.CollectionDb(txCtx).Update(dbCollection)
		if err != nil {
			log.Error("soft delete collection failed", zap.Error(err))
			return fmt.Errorf("collection delete failed due to update error: %w", err)
		}
		return nil
	})
}

func (tc *Catalog) GetSoftDeletedCollections(ctx context.Context, collectionID *string, tenantID string, databaseName string, limit int32) ([]*model.Collection, error) {
	collections, err := tc.metaDomain.CollectionDb(ctx).GetSoftDeletedCollections(collectionID, tenantID, databaseName, limit)
	if err != nil {
		return nil, err
	}
	// Convert to model.Collection
	collectionList := make([]*model.Collection, 0, len(collections))
	for _, dbCollection := range collections {
		collection := &model.Collection{
			ID:           types.MustParse(dbCollection.Collection.ID),
			Name:         *dbCollection.Collection.Name,
			DatabaseName: dbCollection.DatabaseName,
			TenantID:     dbCollection.TenantID,
			Ts:           types.Timestamp(dbCollection.Collection.Ts),
			UpdatedAt:    types.Timestamp(dbCollection.Collection.UpdatedAt.Unix()),
		}
		collectionList = append(collectionList, collection)
	}
	return collectionList, nil
}

func (tc *Catalog) UpdateCollection(ctx context.Context, updateCollection *model.UpdateCollection, ts types.Timestamp) (*model.Collection, error) {
	log.Info("updating collection", zap.String("collectionId", updateCollection.ID.String()))
	var result *model.Collection

	err := tc.txImpl.Transaction(ctx, func(txCtx context.Context) error {
		dbCollection := &dbmodel.Collection{
			ID:        updateCollection.ID.String(),
			Name:      updateCollection.Name,
			Dimension: updateCollection.Dimension,
			Ts:        ts,
		}
		err := tc.metaDomain.CollectionDb(txCtx).Update(dbCollection)
		if err != nil {
			return err
		}

		// Case 1: if ResetMetadata is true, then delete all metadata for the collection
		// Case 2: if ResetMetadata is true and metadata is not nil -> THIS SHOULD NEVER HAPPEN
		// Case 3: if ResetMetadata is false, and the metadata is not nil - set the metadata to the value in metadata
		// Case 4: if ResetMetadata is false and metadata is nil, then leave the metadata as is
		metadata := updateCollection.Metadata
		resetMetadata := updateCollection.ResetMetadata
		if resetMetadata {
			if metadata != nil { // Case 2
				return common.ErrInvalidMetadataUpdate
			} else { // Case 1
				_, err = tc.metaDomain.CollectionMetadataDb(txCtx).DeleteByCollectionID(updateCollection.ID.String())
				if err != nil {
					return err
				}
			}
		} else {
			if metadata != nil { // Case 3
				_, err = tc.metaDomain.CollectionMetadataDb(txCtx).DeleteByCollectionID(updateCollection.ID.String())
				if err != nil {
					return err
				}
				dbCollectionMetadataList := convertCollectionMetadataToDB(updateCollection.ID.String(), metadata)
				if len(dbCollectionMetadataList) != 0 {
					err = tc.metaDomain.CollectionMetadataDb(txCtx).Insert(dbCollectionMetadataList)
					if err != nil {
						return err
					}
				}
			}
		}
		databaseName := updateCollection.DatabaseName
		tenantID := updateCollection.TenantID
		collectionList, err := tc.metaDomain.CollectionDb(txCtx).GetCollections(types.FromUniqueID(updateCollection.ID), nil, tenantID, databaseName, nil, nil)
		if err != nil {
			return err
		}
		if collectionList == nil || len(collectionList) == 0 {
			return common.ErrCollectionNotFound
		}
		result = convertCollectionToModel(collectionList)[0]
		return nil
	})
	if err != nil {
		return nil, err
	}
	log.Info("collection updated", zap.String("collectionID", result.ID.String()))
	return result, nil
}

func (tc *Catalog) CreateSegment(ctx context.Context, createSegment *model.CreateSegment, ts types.Timestamp) (*model.Segment, error) {
	var result *model.Segment

	err := tc.txImpl.Transaction(ctx, func(txCtx context.Context) error {
		var err error
		result, err = tc.createSegmentImpl(txCtx, createSegment, ts)
		return err
	})
	if err != nil {
		log.Error("error creating segment", zap.Error(err))
		return nil, err
	}
	log.Info("segment created", zap.Any("segment", result))
	return result, nil
}

func (tc *Catalog) createSegmentImpl(txCtx context.Context, createSegment *model.CreateSegment, ts types.Timestamp) (*model.Segment, error) {
	var result *model.Segment

	// insert segment
	collectionString := createSegment.CollectionID.String()
	dbSegment := &dbmodel.Segment{
		ID:           createSegment.ID.String(),
		CollectionID: &collectionString,
		Type:         createSegment.Type,
		Scope:        createSegment.Scope,
		Ts:           ts,
	}
	err := tc.metaDomain.SegmentDb(txCtx).Insert(dbSegment)
	if err != nil {
		log.Error("error inserting segment", zap.Error(err))
		return nil, err
	}
	// insert segment metadata
	metadata := createSegment.Metadata
	if metadata != nil {
		dbSegmentMetadataList := convertSegmentMetadataToDB(createSegment.ID.String(), metadata)
		if len(dbSegmentMetadataList) != 0 {
			err = tc.metaDomain.SegmentMetadataDb(txCtx).Insert(dbSegmentMetadataList)
			if err != nil {
				log.Error("error inserting segment metadata", zap.Error(err))
				return nil, err
			}
		}
	}
	// get segment
	segmentList, err := tc.metaDomain.SegmentDb(txCtx).GetSegments(createSegment.ID, nil, nil, createSegment.CollectionID)
	if err != nil {
		log.Error("error getting segment", zap.Error(err))
		return nil, err
	}
	result = convertSegmentToModel(segmentList)[0]

	return result, nil
}

func (tc *Catalog) CreateCollectionAndSegments(ctx context.Context, createCollection *model.CreateCollection, createSegments []*model.CreateSegment, ts types.Timestamp) (*model.Collection, bool, error) {
	var resultCollection *model.Collection
	created := false

	err := tc.txImpl.Transaction(ctx, func(txCtx context.Context) error {
		// Create the collection using the refactored helper
		var err error
		resultCollection, created, err = tc.createCollectionImpl(txCtx, createCollection, ts)
		if err != nil {
			log.Error("error creating collection", zap.Error(err))
			return err
		}

		// If collection already exists, then do not create segments.
		// TODO: Should we check to see if segments does not exist? and create them?
		if !created {
			return nil
		}

		// Create the associated segments.
		for _, createSegment := range createSegments {
			createSegment.CollectionID = resultCollection.ID // Ensure the segment is linked to the newly created collection

			_, err := tc.createSegmentImpl(txCtx, createSegment, ts)
			if err != nil {
				log.Error("error creating segment", zap.Error(err))
				return err
			}
		}

		return nil
	})

	if err != nil {
		log.Error("error creating collection and segments", zap.Error(err))
		return nil, false, err
	}

	log.Info("collection and segments created", zap.Any("collection", resultCollection))
	return resultCollection, created, nil
}

func (tc *Catalog) GetSegments(ctx context.Context, segmentID types.UniqueID, segmentType *string, scope *string, collectionID types.UniqueID) ([]*model.Segment, error) {
	tracer := otel.Tracer
	if tracer != nil {
		_, span := tracer.Start(ctx, "Catalog.GetSegments")
		defer span.End()
	}

	segmentAndMetadataList, err := tc.metaDomain.SegmentDb(ctx).GetSegments(segmentID, segmentType, scope, collectionID)
	if err != nil {
		return nil, err
	}
	segments := make([]*model.Segment, 0, len(segmentAndMetadataList))
	for _, segmentAndMetadata := range segmentAndMetadataList {
		segment := &model.Segment{
			ID:        types.MustParse(segmentAndMetadata.Segment.ID),
			Type:      segmentAndMetadata.Segment.Type,
			Scope:     segmentAndMetadata.Segment.Scope,
			Ts:        segmentAndMetadata.Segment.Ts,
			FilePaths: segmentAndMetadata.Segment.FilePaths,
		}

		if segmentAndMetadata.Segment.CollectionID != nil {
			segment.CollectionID = types.MustParse(*segmentAndMetadata.Segment.CollectionID)
		} else {
			segment.CollectionID = types.NilUniqueID()
		}
		segment.Metadata = convertSegmentMetadataToModel(segmentAndMetadata.SegmentMetadata)
		segments = append(segments, segment)
	}
	return segments, nil
}

// DeleteSegment is a no-op.
// Segments are deleted as part of atomic delete of collection.
// Keeping this API so that older clients continue to work.
func (tc *Catalog) DeleteSegment(ctx context.Context, segmentID types.UniqueID, collectionID types.UniqueID) error {
	return nil
}

func (tc *Catalog) UpdateSegment(ctx context.Context, updateSegment *model.UpdateSegment, ts types.Timestamp) (*model.Segment, error) {
	if updateSegment.Collection == nil {
		return nil, common.ErrMissingCollectionID
	}

	parsedCollectionID, err := types.Parse(*updateSegment.Collection)
	if err != nil {
		return nil, err
	}

	var result *model.Segment

	err = tc.txImpl.Transaction(ctx, func(txCtx context.Context) error {
		{
			results, err := tc.metaDomain.SegmentDb(txCtx).GetSegments(updateSegment.ID, nil, nil, parsedCollectionID)
			if err != nil {
				return err
			}
			if len(results) == 0 {
				return common.ErrSegmentUpdateNonExistingSegment
			}
			updateSegment.Collection = results[0].Segment.CollectionID
		}

		// update segment
		dbSegment := &dbmodel.UpdateSegment{
			ID:         updateSegment.ID.String(),
			Collection: updateSegment.Collection,
		}

		err := tc.metaDomain.SegmentDb(txCtx).Update(dbSegment)
		if err != nil {
			return err
		}

		// Case 1: if ResetMetadata is true, then delete all metadata for the collection
		// Case 2: if ResetMetadata is true and metadata is not nil -> THIS SHOULD NEVER HAPPEN
		// Case 3: if ResetMetadata is false, and the metadata is not nil - set the metadata to the value in metadata
		// Case 4: if ResetMetadata is false and metadata is nil, then leave the metadata as is
		metadata := updateSegment.Metadata
		resetMetadata := updateSegment.ResetMetadata
		if resetMetadata {
			if metadata != nil { // Case 2
				return common.ErrInvalidMetadataUpdate
			} else { // Case 1
				err := tc.metaDomain.SegmentMetadataDb(txCtx).DeleteBySegmentID(updateSegment.ID.String())
				if err != nil {
					return err
				}
			}
		} else {
			if metadata != nil { // Case 3
				err := tc.metaDomain.SegmentMetadataDb(txCtx).DeleteBySegmentIDAndKeys(updateSegment.ID.String(), metadata.Keys())
				if err != nil {
					log.Error("error deleting segment metadata", zap.Error(err))
					return err
				}
				newMetadata := model.NewSegmentMetadata[model.SegmentMetadataValueType]()
				for _, key := range metadata.Keys() {
					if metadata.Get(key) == nil {
						metadata.Remove(key)
					} else {
						newMetadata.Set(key, metadata.Get(key))
					}
				}
				dbSegmentMetadataList := convertSegmentMetadataToDB(updateSegment.ID.String(), newMetadata)
				if len(dbSegmentMetadataList) != 0 {
					err = tc.metaDomain.SegmentMetadataDb(txCtx).Insert(dbSegmentMetadataList)
					if err != nil {
						return err
					}
				}
			}
		}

		// get segment
		segmentList, err := tc.metaDomain.SegmentDb(txCtx).GetSegments(updateSegment.ID, nil, nil, parsedCollectionID)
		if err != nil {
			log.Error("error getting segment", zap.Error(err))
			return err
		}
		result = convertSegmentToModel(segmentList)[0]
		return nil
	})
	if err != nil {
		log.Error("error updating segment", zap.Error(err))
		return nil, err
	}
	log.Debug("segment updated", zap.Any("segment", result))
	return result, nil
}

func (tc *Catalog) SetTenantLastCompactionTime(ctx context.Context, tenantID string, lastCompactionTime int64) error {
	return tc.metaDomain.TenantDb(ctx).UpdateTenantLastCompactionTime(tenantID, lastCompactionTime)
}

func (tc *Catalog) GetTenantsLastCompactionTime(ctx context.Context, tenantIDs []string) ([]*dbmodel.Tenant, error) {
	tenants, err := tc.metaDomain.TenantDb(ctx).GetTenantsLastCompactionTime(tenantIDs)
	return tenants, err
}

func (tc *Catalog) FlushCollectionCompaction(ctx context.Context, flushCollectionCompaction *model.FlushCollectionCompaction) (*model.FlushCollectionInfo, error) {
	flushCollectionInfo := &model.FlushCollectionInfo{
		ID: flushCollectionCompaction.ID.String(),
	}

	err := tc.txImpl.Transaction(ctx, func(txCtx context.Context) error {
		// register files to Segment metadata
		err := tc.metaDomain.SegmentDb(txCtx).RegisterFilePaths(flushCollectionCompaction.FlushSegmentCompactions)
		if err != nil {
			return err
		}

		// update collection log position and version
		collectionVersion, err := tc.metaDomain.CollectionDb(txCtx).UpdateLogPositionAndVersion(flushCollectionCompaction.ID.String(), flushCollectionCompaction.LogPosition, flushCollectionCompaction.CurrentCollectionVersion)
		if err != nil {
			return err
		}
		flushCollectionInfo.CollectionVersion = collectionVersion

		// update tenant last compaction time
		// TODO: add a system configuration to disable
		// since this might cause resource contention if one tenant has a lot of collection compactions at the same time
		lastCompactionTime := time.Now().Unix()
		err = tc.metaDomain.TenantDb(txCtx).UpdateTenantLastCompactionTime(flushCollectionCompaction.TenantID, lastCompactionTime)
		if err != nil {
			return err
		}
		flushCollectionInfo.TenantLastCompactionTime = lastCompactionTime

		// return nil will commit the transaction
		return nil
	})
	if err != nil {
		return nil, err
	}
	return flushCollectionInfo, nil
}
