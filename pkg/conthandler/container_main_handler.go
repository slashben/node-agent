package conthandler

import (
	"errors"
	"fmt"
	"sniffer/pkg/config"
	"sniffer/pkg/context"
	v1 "sniffer/pkg/conthandler/v1"
	accumulator "sniffer/pkg/event_data_storage"
	"sniffer/pkg/sbom"
	"sniffer/pkg/storageclient"
	"strings"
	"sync"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
)

const (
	RelevantCVEsService = "RelevantCVEsService"
	StepGetSBOM         = "StepGetSBOM"
	StepEventAggregator = "StepEventAggregator"
)

type supportedServices string

type afterTimerActionsData struct {
	containerID string
	service     supportedServices
}

type watchedContainerData struct {
	containerAggregator *Aggregator
	snifferTicker       *time.Ticker
	event               v1.ContainerEventData
	syncChannel         map[string]chan error
	sbomClient          sbom.SBOMClient
}

type ContainerHandler struct {
	containerWatcher    *ContainerWatcher
	containersEventChan chan v1.ContainerEventData
	// watchedContainers        map[string]watchedContainerData
	// syncWatchedContainersMap *sync.RWMutex
	watchedContainers        sync.Map
	afterTimerActionsChannel chan afterTimerActionsData
	storageClient            storageclient.StorageClient
}

func CreateContainerHandler(contClient ContainerClient, storageClient storageclient.StorageClient) (*ContainerHandler, error) {

	contWatcher, err := CreateContainerWatcher(contClient)
	if err != nil {
		return nil, err
	}

	return &ContainerHandler{
		containersEventChan: make(chan v1.ContainerEventData, 50),
		containerWatcher:    contWatcher,
		watchedContainers:   sync.Map{},
		// syncWatchedContainersMap: &sync.RWMutex{},
		afterTimerActionsChannel: make(chan afterTimerActionsData, 50),
		storageClient:            storageClient,
	}, nil
}

func (ch *ContainerHandler) afterTimerActions() error {
	var err error

	for {
		afterTimerActionsData := <-ch.afterTimerActionsChannel
		containerDataInterface, exist := ch.watchedContainers.Load(afterTimerActionsData.containerID)
		if !exist {
			logger.L().Ctx(context.GetBackgroundContext()).Warning("afterTimerActions: failed to get container data of container ID", []helpers.IDetails{helpers.String("container ID", afterTimerActionsData.containerID)}...)
			continue
		}
		containerData := containerDataInterface.(watchedContainerData)

		if config.GetConfigurationConfigContext().IsRelevantCVEServiceEnabled() && afterTimerActionsData.service == RelevantCVEsService {
			fileList := containerData.containerAggregator.GetContainerRealtimeFileList()

			if err = <-containerData.syncChannel[StepGetSBOM]; err != nil {
				logger.L().Ctx(context.GetBackgroundContext()).Warning("failed to get SBOM", []helpers.IDetails{helpers.String("container ID", afterTimerActionsData.containerID), helpers.String("container name", containerData.event.GetContainerName()), helpers.String("k8s resource ", containerData.event.GetK8SWorkloadID()), helpers.Error(err)}...)
				continue
			}
			if err = containerData.sbomClient.FilterSBOM(fileList); err != nil {
				logger.L().Ctx(context.GetBackgroundContext()).Warning("failed to filter SBOM", []helpers.IDetails{helpers.String("container ID", afterTimerActionsData.containerID), helpers.String("container name", containerData.event.GetContainerName()), helpers.String("k8s resource", containerData.event.GetK8SWorkloadID()), helpers.Error(err)}...)
				continue
			}
			if err = containerData.sbomClient.StoreFilterSBOM(containerData.event.GetInstanceIDHash()); err != nil {
				if errors.Is(err, sbom.IsAlreadyExist()) {
					logger.L().Info("SBOM already reported", []helpers.IDetails{helpers.String("container ID", afterTimerActionsData.containerID), helpers.String("container name", containerData.event.GetContainerName()), helpers.String("k8s resource", containerData.event.GetK8SWorkloadID())}...)
				} else {
					logger.L().Ctx(context.GetBackgroundContext()).Warning("failed to store filter SBOM", []helpers.IDetails{helpers.String("container ID", afterTimerActionsData.containerID), helpers.String("k8s resource", containerData.event.GetK8SWorkloadID()), helpers.Error(err)}...)
				}
				continue
			}
			logger.L().Info("filtered SBOM has stored successfully in the storage", []helpers.IDetails{helpers.String("containerID", afterTimerActionsData.containerID), helpers.String("k8s resource", containerData.event.GetK8SWorkloadID())}...)
		}
	}
}

func (ch *ContainerHandler) startTimer(watchedContainer watchedContainerData, containerID string) error {
	var err error
	select {
	case <-watchedContainer.snifferTicker.C:
		if config.GetConfigurationConfigContext().IsRelevantCVEServiceEnabled() {
			ch.afterTimerActionsChannel <- afterTimerActionsData{
				containerID: containerID,
				service:     RelevantCVEsService,
			}
		}
	case err = <-watchedContainer.syncChannel[StepEventAggregator]:
		if err.Error() == accumulator.DropEventOccurred {
			watchedContainer.snifferTicker.Stop()
			err = fmt.Errorf("we have missed some kernel events, we are going to stop all current containers monitoring")
		}
	}

	return err
}

func createTicker() *time.Ticker {
	return time.NewTicker(config.GetConfigurationConfigContext().GetUpdateDataPeriod())
}

func (ch *ContainerHandler) startRelevancyProcess(contEvent v1.ContainerEventData) {
	containerDataInterface, exist := ch.watchedContainers.Load(contEvent.GetContainerID())
	if !exist {
		logger.L().Ctx(context.GetBackgroundContext()).Error("startRelevancyProcess: failed to get container data", helpers.String("container ID", contEvent.GetContainerID()), helpers.String("container name", contEvent.GetContainerName()), helpers.String("k8s resources", contEvent.GetK8SWorkloadID()))
		return
	}
	watchedContainer := containerDataInterface.(watchedContainerData)

	err := watchedContainer.containerAggregator.StartAggregate(watchedContainer.syncChannel[StepEventAggregator])
	if err != nil {
		return
	}

	now := time.Now()
	configStopTime := config.GetConfigurationConfigContext().GetSniffingMaxTimes()
	stopSniffingTime := now.Add(configStopTime)
	for start := time.Now(); start.Before(stopSniffingTime); {
		go ch.getSBOM(contEvent)
		err = ch.startTimer(watchedContainer, contEvent.GetContainerID())
		if err != nil {
			logger.L().Ctx(context.GetBackgroundContext()).Warning("timer of containerID stop before expected", helpers.String("container ID", contEvent.GetContainerID()), helpers.String("container name", contEvent.GetContainerName()), helpers.String("k8s resources", contEvent.GetK8SWorkloadID()), helpers.Error(err))
			err = watchedContainer.containerAggregator.StopAggregate()
			if err != nil {
				logger.L().Ctx(context.GetBackgroundContext()).Warning("we have failed to stop to aggregate data", helpers.String("container ID", contEvent.GetContainerID()), helpers.String("container name", contEvent.GetContainerName()), helpers.String("k8s resources", contEvent.GetK8SWorkloadID()))
			}
			ch.watchedContainers.Delete(contEvent.GetContainerID())
			break
		}
	}
}

func getShortContainerID(containerID string) string {
	cont := strings.Split(containerID, "://")
	return cont[1][:12]
}

func (ch *ContainerHandler) getSBOM(contEvent v1.ContainerEventData) {
	containerDataInterface, exist := ch.watchedContainers.Load(contEvent.GetContainerID())
	if !exist {
		logger.L().Ctx(context.GetBackgroundContext()).Error("getSBOM: failed to get container data of ContainerID, not exist in memory", helpers.String("containerID", contEvent.GetContainerID()))
		return
	}
	watchedContainer := containerDataInterface.(watchedContainerData)
	imageHash, err := contEvent.GetImageHash()
	if err == nil {
		err = watchedContainer.sbomClient.GetSBOM(imageHash)
	}
	watchedContainer.syncChannel[StepGetSBOM] <- err
}

func (ch *ContainerHandler) handleContainerRunningEvent(contEvent v1.ContainerEventData) error {
	newWatchedContainer := watchedContainerData{
		containerAggregator: CreateAggregator(getShortContainerID(contEvent.GetContainerID())),
		snifferTicker:       createTicker(),
		event:               contEvent,
		sbomClient:          sbom.CreateSBOMStorageClient(ch.storageClient, contEvent.GetK8SWorkloadID(), contEvent.GetInstanceID()),
		syncChannel: map[string]chan error{
			StepGetSBOM:         make(chan error, 10),
			StepEventAggregator: make(chan error, 10),
		},
	}
	ch.watchedContainers.Store(contEvent.GetContainerID(), newWatchedContainer)
	go ch.startRelevancyProcess(contEvent)
	return nil
}

func (ch *ContainerHandler) handleNewContainerEvent(contEvent v1.ContainerEventData) error {
	switch contEvent.GetContainerEventType() {
	case v1.ContainerRunning:
		return ch.handleContainerRunningEvent(contEvent)
	}
	return nil
}

func (ch *ContainerHandler) StartMainHandler() error {
	go ch.afterTimerActions()
	go ch.containerWatcher.StartWatchedOnContainers(ch.containersEventChan)

	for {
		contEvent := <-ch.containersEventChan
		logger.L().Info("", []helpers.IDetails{helpers.String("new container has loaded", contEvent.GetContainerID()), helpers.String("ContainerID", contEvent.GetContainerID()), helpers.String("Container name", contEvent.GetContainerID()), helpers.String("k8s workload", contEvent.GetK8SWorkloadID())}...)
		err := ch.handleNewContainerEvent(contEvent)
		if err != nil {
			logger.L().Ctx(context.GetBackgroundContext()).Warning("fail to handle new container", helpers.String("ContainerID", contEvent.GetContainerID()), helpers.String("Container name", contEvent.GetContainerID()), helpers.String("k8s workload", contEvent.GetK8SWorkloadID()), helpers.Error(err))
		}
	}
}
