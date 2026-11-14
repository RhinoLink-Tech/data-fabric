package main

import (
    "context"
    "os"
    "strings"
    "sync"
    "testing"

    "github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
    "github.com/RhinoLink-Tech/data-fabric/internal/database"
    "github.com/RhinoLink-Tech/data-fabric/internal/model"
    "github.com/RhinoLink-Tech/data-fabric/internal/repository"
    "github.com/RhinoLink-Tech/data-fabric/internal/security"
    "github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
    "gorm.io/driver/mysql"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"
)

// This test creates tables and must only target a fresh disposable database.
func TestSelfServiceMySQLConcurrentProvisioning(t *testing.T) {
    dsn:=os.Getenv("DF_SELF_SERVICE_TEST_MYSQL_DSN")
    if dsn=="" {t.Skip("disposable MySQL DSN not set")}
    db,err:=gorm.Open(mysql.Open(dsn),&gorm.Config{Logger:logger.Default.LogMode(logger.Silent)})
    if err!=nil{t.Fatal("cannot open disposable MySQL")}
    defer func(){if sqlDB,e:=db.DB();e==nil{_ = sqlDB.Close()}}()
    if err=db.Exec("CREATE TABLE data_sources (id varchar(36) primary key, name varchar(255), type varchar(80), config json not null, status varchar(20), owner_id varchar(255) not null default '', tenant_id varchar(255) not null default '', created_at datetime(6), updated_at datetime(6), UNIQUE KEY tenant_name (tenant_id,name)) ENGINE=InnoDB").Error;err!=nil{t.Fatal(err)}
    if err=onboarding.MigrateGrants(db);err!=nil{t.Fatal(err)}
    if err=auditlog.Migrate(db);err!=nil{t.Fatal(err)}
    vault,_:=security.NewCredentialVault([]byte(strings.Repeat("k",32)))
    policy,_:=database.NewFixedIPEgressPolicy([]string{"10.254.81.10/32"},[]int{5432})
    repo,_:=repository.NewDataSourceRepository(db,vault)
    sample:=&model.DataSource{Name:"Template",Type:model.DatabaseTypePostgreSQL,Status:model.DataSourceStatusActive,OwnerID:"operator",TenantID:"original",Config:model.DataSourceConfig{Host:"10.254.81.10",Port:5432,Database:"synthetic_orders",Username:"demo_reader",Password:"synthetic-password"}}
    ctx:=context.Background();key:=strings.Repeat("k",32);issuer:="https://issuer.example"
    if err=repo.Create(ctx,sample);err!=nil{t.Fatal(err)}
    provision:=func()error{return provisionSelfServiceGrant(ctx,db,issuer,"alice","tenant-a",key,func(ctx context.Context,tx *gorm.DB)error{
        _,err:=seedSyntheticForSubject(ctx,tx,vault,policy,issuer,"alice","tenant-a",sample.ID,"self-service",key);return err
    })}
    var wg sync.WaitGroup
    for i:=0;i<8;i++{wg.Add(1);go func(){defer wg.Done();_ = provision()}()}
    wg.Wait()
    if err=provision();err!=nil{t.Fatalf("fresh retry failed: %v",err)}
    var count int64
    if err=db.Table("data_sources").Where("tenant_id = ? AND owner_id = ?","tenant-a","alice").Count(&count).Error;err!=nil || count!=1{t.Fatalf("sample count=%d",count)}
    store,_:=onboarding.NewGrantStore(db)
    if _,tenant,ok,_:=store.Resolve(ctx,issuer,"alice");!ok || tenant!="tenant-a"{t.Fatal("grant missing")}
    if err=store.Revoke(ctx,issuer,"alice");err!=nil{t.Fatal(err)}
    if err=provision();err==nil{t.Fatal("revoked grant was restored")}
    if err=provisionSelfServiceGrant(ctx,db,issuer,"bob","tenant-b",key,func(context.Context,*gorm.DB)error{return gorm.ErrInvalidData});err==nil{t.Fatal("seed failure accepted")}
    if _,_,ok,_:=store.Resolve(ctx,issuer,"bob");ok{t.Fatal("grant survived seed failure")}
}
