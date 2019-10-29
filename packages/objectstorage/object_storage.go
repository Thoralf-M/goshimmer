package objectstorage

import (
	"sync"

	"github.com/dgraph-io/badger"
)

type ObjectStorage struct {
	badgerInstance *badger.DB
	storageId      []byte
	objectFactory  StorableObjectFactory
	cachedObjects  map[string]*CachedObject
	cacheMutex     sync.RWMutex
	options        *ObjectStorageOptions
}

func New(storageId string, objectFactory StorableObjectFactory, optionalOptions ...ObjectStorageOption) *ObjectStorage {
	return &ObjectStorage{
		badgerInstance: GetBadgerInstance(),
		storageId:      []byte(storageId),
		objectFactory:  objectFactory,
		cachedObjects:  map[string]*CachedObject{},
		options:        newTransportOutputStorageFilters(optionalOptions),
	}
}

func (objectStorage *ObjectStorage) Store(object StorableObject) *CachedObject {
	return objectStorage.accessCache(object.GetStorageKey(), func(cachedObject *CachedObject) {
		if !cachedObject.publishResult(object, nil) {
			if currentValue := cachedObject.Get(); currentValue != nil {
				currentValue.Update(object)
			} else {
				cachedObject.updateValue(object)
			}
		}
	}, func(cachedObject *CachedObject) {
		cachedObject.publishResult(object, nil)
	})
}

func (objectStorage *ObjectStorage) Load(key []byte) (*CachedObject, error) {
	return objectStorage.accessCache(key, nil, func(cachedObject *CachedObject) {
		cachedObject.publishResult(objectStorage.loadObjectFromBadger(key))
	}).waitForResult()
}

func (objectStorage *ObjectStorage) Delete(key []byte) {
	objectStorage.accessCache(key, func(cachedObject *CachedObject) {
		cachedObject.Delete()
		cachedObject.Release()
	}, func(cachedObject *CachedObject) {
		cachedObject.Delete()
		cachedObject.publishResult(nil, nil)
		cachedObject.Release()
	})
}

func (objectStorage *ObjectStorage) ForEach(consumer func(key []byte, cachedObject *CachedObject) bool, optionalPrefixes ...[]byte) error {
	return objectStorage.badgerInstance.View(func(txn *badger.Txn) error {
		iteratorOptions := badger.DefaultIteratorOptions
		iteratorOptions.Prefix = objectStorage.generatePrefix(optionalPrefixes)

		it := txn.NewIterator(iteratorOptions)
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			key := item.Key()[len(objectStorage.storageId):]

			if cachedObject, err := objectStorage.accessCache(key, nil, func(cachedObject *CachedObject) {
				_ = item.Value(func(val []byte) error {
					marshaledData := make([]byte, len(val))
					copy(marshaledData, val)

					cachedObject.publishResult(objectStorage.unmarshalObject(key, marshaledData))

					return nil
				})
			}).waitForResult(); err != nil {
				it.Close()

				return err
			} else {
				if !consumer(key, cachedObject) {
					break
				}
			}
		}
		it.Close()

		return nil
	})
}

func (objectStorage *ObjectStorage) Prune() error {
	objectStorage.cacheMutex.Lock()
	if err := objectStorage.badgerInstance.DropPrefix(objectStorage.storageId); err != nil {
		return err
	}
	objectStorage.cachedObjects = map[string]*CachedObject{}
	objectStorage.cacheMutex.Unlock()

	return nil
}

func (objectStorage *ObjectStorage) accessCache(key []byte, onCacheHit func(*CachedObject), onCacheMiss func(*CachedObject)) *CachedObject {
	stringKey := string(key)

	objectStorage.cacheMutex.RLock()
	cachedObject, cachedObjectExists := objectStorage.cachedObjects[stringKey]
	if cachedObjectExists {
		cachedObject.RegisterConsumer()

		objectStorage.cacheMutex.RUnlock()

		if onCacheHit != nil {
			onCacheHit(cachedObject)
		}
	} else {
		objectStorage.cacheMutex.RUnlock()
		objectStorage.cacheMutex.Lock()
		if cachedObject, cachedObjectExists = objectStorage.cachedObjects[stringKey]; cachedObjectExists {
			cachedObject.RegisterConsumer()

			objectStorage.cacheMutex.Unlock()

			if onCacheHit != nil {
				onCacheHit(cachedObject)
			}
		} else {
			cachedObject = newCachedObject(objectStorage)
			cachedObject.RegisterConsumer()

			objectStorage.cachedObjects[stringKey] = cachedObject
			objectStorage.cacheMutex.Unlock()

			if onCacheMiss != nil {
				onCacheMiss(cachedObject)
			}
		}
	}

	return cachedObject
}

func (objectStorage *ObjectStorage) persistObjectToBadger(key []byte, value StorableObject) error {
	if value != nil {
		return objectStorage.badgerInstance.Update(func(txn *badger.Txn) error {
			marshaledObject, _ := value.MarshalBinary()

			return txn.Set(objectStorage.generatePrefix([][]byte{key}), marshaledObject)
		})
	}

	return nil
}

func (objectStorage *ObjectStorage) deleteObjectFromBadger(key []byte) error {
	return objectStorage.badgerInstance.Update(func(txn *badger.Txn) error {
		return txn.Delete(objectStorage.generatePrefix([][]byte{key}))
	})
}

func (objectStorage *ObjectStorage) loadObjectFromBadger(key []byte) (StorableObject, error) {
	var marshaledData []byte
	if err := objectStorage.badgerInstance.View(func(txn *badger.Txn) error {
		if item, err := txn.Get(append(objectStorage.storageId, key...)); err != nil {
			return err
		} else {
			return item.Value(func(val []byte) error {
				marshaledData = make([]byte, len(val))
				copy(marshaledData, val)

				return nil
			})
		}
	}); err != nil {
		if err == badger.ErrKeyNotFound {
			return nil, nil
		} else {
			return nil, err
		}
	} else {
		return objectStorage.unmarshalObject(key, marshaledData)
	}
}

func (objectStorage *ObjectStorage) unmarshalObject(key []byte, data []byte) (StorableObject, error) {
	object := objectStorage.objectFactory(key)
	if err := object.UnmarshalBinary(data); err != nil {
		return nil, err
	} else {
		return object, nil
	}
}

func (objectStorage *ObjectStorage) generatePrefix(optionalPrefixes [][]byte) (prefix []byte) {
	prefix = objectStorage.storageId
	for _, optionalPrefix := range optionalPrefixes {
		prefix = append(prefix, optionalPrefix...)
	}

	return
}