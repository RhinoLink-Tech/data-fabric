package service

import (
	"context"
	"errors"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
)

type datasourceConfigValidatorStub struct {
	supported     bool
	validationErr error
	calls         int
	lastType      model.DatabaseType
}

func (v *datasourceConfigValidatorStub) IsSupportedDataSourceType(model.DatabaseType) bool {
	return v.supported
}

func (v *datasourceConfigValidatorStub) ValidateDataSourceConfiguration(_ *model.DataSourceConfig, dbType model.DatabaseType) error {
	v.calls++
	v.lastType = dbType
	return v.validationErr
}

type validatingDataSourceRepositoryStub struct {
	repository.DataSourceRepository
	existing *model.DataSource
	created  *model.DataSource
	updated  *model.DataSource
}

func (r *validatingDataSourceRepositoryStub) GetByName(context.Context, string, string) (*model.DataSource, error) {
	return nil, repository.ErrDataSourceNotFound
}
func (r *validatingDataSourceRepositoryStub) Create(_ context.Context, dataSource *model.DataSource) error {
	r.created = dataSource
	return nil
}
func (r *validatingDataSourceRepositoryStub) GetByID(context.Context, string) (*model.DataSource, error) {
	return r.existing, nil
}
func (r *validatingDataSourceRepositoryStub) Update(_ context.Context, dataSource *model.DataSource) error {
	r.updated = dataSource
	return nil
}

func TestCreateDataSourceValidatesTypeAndConfigBeforePersistence(t *testing.T) {
	validRequest := &CreateDataSourceRequest{
		Name: "warehouse", Type: model.DatabaseTypeSQLServer,
		Config: model.DataSourceConfig{Host: "db.example.test", Database: "analytics", Username: "gateway"},
	}

	t.Run("rejects unregistered type", func(t *testing.T) {
		repo := &validatingDataSourceRepositoryStub{}
		validator := &datasourceConfigValidatorStub{supported: false}
		svc := &dataSourceService{repo: repo, configValidator: validator}
		_, err := svc.CreateDataSource(context.Background(), nil, validRequest)
		if !errors.Is(err, ErrUnsupportedDataSourceType) {
			t.Fatalf("CreateDataSource() error = %v, want ErrUnsupportedDataSourceType", err)
		}
		if repo.created != nil || validator.calls != 0 {
			t.Fatal("unsupported type must be rejected before config validation and persistence")
		}
	})

	t.Run("rejects invalid driver config", func(t *testing.T) {
		repo := &validatingDataSourceRepositoryStub{}
		validator := &datasourceConfigValidatorStub{supported: true, validationErr: errors.New("host is required")}
		svc := &dataSourceService{repo: repo, configValidator: validator}
		_, err := svc.CreateDataSource(context.Background(), nil, validRequest)
		if !errors.Is(err, ErrInvalidDataSourceConfig) {
			t.Fatalf("CreateDataSource() error = %v, want ErrInvalidDataSourceConfig", err)
		}
		if repo.created != nil || validator.calls != 1 {
			t.Fatal("invalid config must be validated and rejected before persistence")
		}
	})

	t.Run("persists valid registered driver config", func(t *testing.T) {
		repo := &validatingDataSourceRepositoryStub{}
		validator := &datasourceConfigValidatorStub{supported: true}
		svc := &dataSourceService{repo: repo, configValidator: validator}
		if _, err := svc.CreateDataSource(context.Background(), nil, validRequest); err != nil {
			t.Fatalf("CreateDataSource() error = %v", err)
		}
		if repo.created == nil || validator.calls != 1 || validator.lastType != model.DatabaseTypeSQLServer {
			t.Fatal("expected registered type to be validated before persistence")
		}
	})
}

func TestUpdateDataSourceValidatesExistingTypeAndFinalConfigBeforePersistence(t *testing.T) {
	dataSource := &model.DataSource{
		ID:     "e8c3cfb8-21dd-4f29-a197-7a616e0165ba",
		Type:   model.DatabaseTypeSQLServer,
		Config: model.DataSourceConfig{Host: "db.example.test", Database: "analytics", Username: "gateway"},
	}
	repo := &validatingDataSourceRepositoryStub{existing: dataSource}
	validator := &datasourceConfigValidatorStub{supported: true, validationErr: errors.New("host is required")}
	svc := &dataSourceService{repo: repo, configValidator: validator}

	_, err := svc.UpdateDataSource(context.Background(), nil, dataSource.ID, &UpdateDataSourceRequest{
		Config: &model.DataSourceConfig{Host: "", Database: "analytics", Username: "gateway"},
	})
	if !errors.Is(err, ErrInvalidDataSourceConfig) {
		t.Fatalf("UpdateDataSource() error = %v, want ErrInvalidDataSourceConfig", err)
	}
	if repo.updated != nil || validator.calls != 1 || validator.lastType != model.DatabaseTypeSQLServer {
		t.Fatal("update must validate its final config using the stored source type before persistence")
	}
}

func TestCreateDataSourceRejectsUnimplementedOzoneTypesBeforePersistence(t *testing.T) {
	repo := &validatingDataSourceRepositoryStub{}
	svc := NewDataSourceService(repo, nil, database.NewHealthChecker(nil))
	ozoneTypes := []model.DatabaseType{
		model.DatabaseTypeOzoneParquet,
		model.DatabaseTypeOzoneText,
		model.DatabaseTypeOzoneAvro,
	}
	for _, dbType := range ozoneTypes {
		t.Run(string(dbType), func(t *testing.T) {
			_, err := svc.CreateDataSource(context.Background(), nil, &CreateDataSourceRequest{
				Name: "unsupported-ozone", Type: dbType,
				Config: model.DataSourceConfig{Host: "ozone.example.test"},
			})
			if !errors.Is(err, ErrUnsupportedDataSourceType) {
				t.Fatalf("CreateDataSource() error = %v, want unsupported-type error", err)
			}
			if repo.created != nil {
				t.Fatalf("unsupported Ozone source was persisted: %+v", repo.created)
			}
		})
	}
}

func TestUpdateDataSourceRejectsUnimplementedOzoneTypesBeforePersistence(t *testing.T) {
	ozoneTypes := []model.DatabaseType{
		model.DatabaseTypeOzoneParquet,
		model.DatabaseTypeOzoneText,
		model.DatabaseTypeOzoneAvro,
	}
	for _, dbType := range ozoneTypes {
		t.Run(string(dbType), func(t *testing.T) {
			const id = "e8c3cfb8-21dd-4f29-a197-7a616e0165ba"
			repo := &validatingDataSourceRepositoryStub{
				existing: &model.DataSource{
					ID: id, Type: dbType,
					Config: model.DataSourceConfig{Host: "ozone.example.test"},
				},
			}
			svc := NewDataSourceService(repo, nil, database.NewHealthChecker(nil))
			_, err := svc.UpdateDataSource(context.Background(), nil, id, &UpdateDataSourceRequest{
				Config: &model.DataSourceConfig{Host: "ozone.example.test"},
			})
			if !errors.Is(err, ErrUnsupportedDataSourceType) {
				t.Fatalf("UpdateDataSource() error = %v, want unsupported-type error", err)
			}
			if repo.updated != nil {
				t.Fatalf("unsupported Ozone source was updated: %+v", repo.updated)
			}
		})
	}
}
